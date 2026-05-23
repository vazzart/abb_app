package adb

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/electricbubble/gadb"
	"go.uber.org/zap"

	"abb/internal/model"
)

var (
	// reRowSplit splits ADB output on each "Row: N " line header.
	reRowSplit = regexp.MustCompile(`(?m)^Row: \d+ `)

	reID = regexp.MustCompile(`_id=(\d+)`)

	// reDateSuffix matches ", date=<digits>" anchored to end of string (not line).
	// Handles multiline bodies: date is always the last token in a row.
	reDateSuffix = regexp.MustCompile(`,\s*date=(\d+)\s*$`)
)

// Poller periodically reads new SMS via the ADB server and emits them on Messages.
type Poller struct {
	client   gadb.Client
	serial   string
	interval time.Duration
	lastID   int64
	log      *zap.Logger
	Messages chan model.Message
}

// FetchMaxAndroidID queries the device's SMS inbox and returns the current
// maximum android_id. Used at startup to skip all SMS already on the device.
func FetchMaxAndroidID(device *gadb.Device) (int64, error) {
	out, err := device.RunShellCommand(smsShellCmd)
	if err != nil {
		return 0, fmt.Errorf("adb shell: %w", err)
	}
	msgs, err := ParseSMSOutput(out)
	if err != nil {
		return 0, fmt.Errorf("parse sms: %w", err)
	}
	var maxID int64
	for _, msg := range msgs {
		id, _ := strconv.ParseInt(msg.AndroidID, 10, 64)
		if id > maxID {
			maxID = id
		}
	}
	return maxID, nil
}

// NewPoller creates a new SMS poller. initialLastID seeds the deduplication
// cursor so already-existing SMS are skipped.
func NewPoller(client gadb.Client, serial string, interval time.Duration, initialLastID int64, log *zap.Logger) *Poller {
	return &Poller{
		client:   client,
		serial:   serial,
		interval: interval,
		lastID:   initialLastID,
		log:      log,
		Messages: make(chan model.Message, 100),
	}
}

func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

// smsShellCmd is passed as a single command string to the Android shell so that
// single-quoted arguments (e.g. 'date DESC') are handled correctly by the shell.
const smsShellCmd = "content query --uri content://sms/inbox --projection '_id,address,body,date' --sort 'date DESC'"

func (p *Poller) poll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	devices, err := p.client.DeviceList()
	if err != nil {
		p.log.Error("adb DeviceList failed", zap.Error(err))
		return
	}

	var device *gadb.Device
	for i := range devices {
		if devices[i].Serial() == p.serial {
			device = &devices[i]
			break
		}
	}
	if device == nil {
		p.log.Warn("device not found in list", zap.String("serial", p.serial))
		return
	}

	out, err := device.RunShellCommand(smsShellCmd)
	if err != nil {
		p.log.Error("adb content query failed", zap.Error(err))
		return
	}

	msgs, err := ParseSMSOutput(out)
	if err != nil {
		p.log.Error("parse sms output failed", zap.Error(err))
		return
	}

	var maxID int64
	for _, msg := range msgs {
		id, _ := strconv.ParseInt(msg.AndroidID, 10, 64)
		if id > maxID {
			maxID = id
		}
		if id <= p.lastID {
			continue
		}
		select {
		case p.Messages <- msg:
		case <-ctx.Done():
			return
		}
	}

	if maxID > p.lastID {
		p.lastID = maxID
	}
}

// ParseSMSOutput parses the raw output of "content query --uri content://sms/inbox".
// Handles multiline message bodies: body spans until the ", date=<digits>" suffix.
func ParseSMSOutput(raw string) ([]model.Message, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "No result found") {
		return nil, nil
	}

	parts := reRowSplit.Split(raw, -1)
	var msgs []model.Message
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		msg, err := parseRow(part)
		if err != nil {
			return nil, fmt.Errorf("parseRow: %w", err)
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func parseRow(row string) (model.Message, error) {
	idMatch := reID.FindStringSubmatch(row)
	if idMatch == nil {
		return model.Message{}, fmt.Errorf("_id not found in row: %.80s", row)
	}

	dateMatch := reDateSuffix.FindStringSubmatch(row)
	if dateMatch == nil {
		return model.Message{}, fmt.Errorf("date not found in row _id=%s", idMatch[1])
	}
	dateMs, _ := strconv.ParseInt(dateMatch[1], 10, 64)

	// address is between "address=" and ", body="
	addrStart := strings.Index(row, "address=")
	bodyTag := strings.Index(row, ", body=")
	if addrStart < 0 || bodyTag < 0 {
		return model.Message{}, fmt.Errorf("address/body fields not found in row _id=%s", idMatch[1])
	}
	address := row[addrStart+len("address=") : bodyTag]

	// body is between "body=" and the trailing ", date=<digits>"
	bodyStart := bodyTag + len(", body=")
	dateTagIdx := reDateSuffix.FindStringIndex(row)
	body := row[bodyStart:dateTagIdx[0]]

	return model.Message{
		AndroidID:  idMatch[1],
		Address:    address,
		Body:       body,
		ReceivedAt: time.UnixMilli(dateMs),
	}, nil
}
