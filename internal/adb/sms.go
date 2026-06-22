package adb

import (
	"context"
	"errors"
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

	reSubscriptionID = regexp.MustCompile(`subscription_id=(-?\d+)`)
)

// FetchSimInfo queries the device's SIM info table and returns a map of
// subscription_id (the SMS "subscription_id" field) to a human-readable name.
// On devices that don't expose siminfo the function returns an empty map.
func FetchSimInfo(device *gadb.Device) (map[string]string, error) {
	out, err := device.RunShellCommand(simInfoShellCmd)
	if err != nil {
		return nil, fmt.Errorf("adb siminfo: %w", err)
	}
	return parseSimInfo(out), nil
}

var (
	reSimID          = regexp.MustCompile(`_id=(\d+)`)
	reSimDisplayName = regexp.MustCompile(`display_name=([^,]+)`)
	reSimSlot        = regexp.MustCompile(`sim_slot_index=(-?\d+)`)
)

func parseSimInfo(raw string) map[string]string {
	result := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "No result") || strings.HasPrefix(raw, "Exception") {
		return result
	}
	parts := reRowSplit.Split(raw, -1)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idM := reSimID.FindStringSubmatch(part)
		nameM := reSimDisplayName.FindStringSubmatch(part)
		slotM := reSimSlot.FindStringSubmatch(part)
		if idM == nil {
			continue
		}
		name := ""
		if nameM != nil {
			name = strings.TrimSpace(nameM[1])
		}
		if name == "" && slotM != nil {
			name = "SIM " + slotM[1]
		}
		if name == "" {
			name = "SIM " + idM[1]
		}
		result[idM[1]] = name
	}
	return result
}

// Poller periodically reads new SMS via the ADB server and emits them on Messages.
type Poller struct {
	client     gadb.Client
	serial     string
	deviceName string
	interval   time.Duration
	lastID     int64
	simInfo    map[string]string
	compatMode bool // true when device lacks subscription_id column
	log        *zap.Logger
	Messages   chan model.Message
}

// FetchMaxAndroidID queries the device's SMS inbox and returns the current
// maximum android_id. Used at startup to skip all SMS already on the device.
func FetchMaxAndroidID(device *gadb.Device) (int64, error) {
	out, err := device.RunShellCommand(smsShellCmd)
	if err != nil {
		return 0, fmt.Errorf("adb shell: %w", err)
	}
	msgs, err := ParseSMSOutput(out)
	if errors.Is(err, errSQLite) {
		// Device doesn't support subscription_id — retry with compat query.
		out, err = device.RunShellCommand(smsShellCmdCompat)
		if err != nil {
			return 0, fmt.Errorf("adb shell compat: %w", err)
		}
		msgs, err = ParseSMSOutput(out)
	}
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
// cursor so already-existing SMS are skipped. deviceName is set on every
// message emitted so receivers know which phone the SMS came from.
// simInfo maps subscription_id strings to SIM display names; may be nil.
func NewPoller(client gadb.Client, serial, deviceName string, interval time.Duration, initialLastID int64, simInfo map[string]string, log *zap.Logger) *Poller {
	if simInfo == nil {
		simInfo = make(map[string]string)
	}
	return &Poller{
		client:     client,
		serial:     serial,
		deviceName: deviceName,
		interval:   interval,
		lastID:     initialLastID,
		simInfo:    simInfo,
		log:        log,
		Messages:   make(chan model.Message, 100),
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

// smsShellCmd queries SMS inbox with subscription_id (Android 5.1+).
// subscription_id is placed before body so the body parser works correctly.
const smsShellCmd = "content query --uri content://sms/inbox --projection '_id,subscription_id,address,body,date' --sort 'date DESC'"

// smsShellCmdCompat is a fallback for devices where subscription_id column is missing.
const smsShellCmdCompat = "content query --uri content://sms/inbox --projection '_id,address,body,date' --sort 'date DESC'"

// simInfoShellCmd queries the SIM card info table.
const simInfoShellCmd = "content query --uri content://telephony/siminfo --projection '_id,display_name,sim_slot_index'"

// errSQLite is returned by ParseSMSOutput when the ADB output contains a SQLite error,
// signalling the caller to retry with a compatible query.
var errSQLite = fmt.Errorf("sqlite error in adb output")

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

	cmd := smsShellCmd
	if p.compatMode {
		cmd = smsShellCmdCompat
	}
	out, err := device.RunShellCommand(cmd)
	if err != nil {
		p.log.Error("adb content query failed", zap.Error(err))
		return
	}

	msgs, err := ParseSMSOutput(out)
	if err != nil {
		if !p.compatMode && errors.Is(err, errSQLite) {
			p.log.Warn("subscription_id not supported on this device, switching to compat mode")
			p.logFirstSMSRow(device)
			p.compatMode = true
			return
		}
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
		msg.DeviceName = p.deviceName
		if msg.SubscriptionID != "" {
			if name, ok := p.simInfo[msg.SubscriptionID]; ok {
				msg.SimName = name
			}
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

// logFirstSMSRow queries the SMS inbox without a projection and logs the first
// row verbatim so we can see all available column names on this device.
func (p *Poller) logFirstSMSRow(device *gadb.Device) {
	out, err := device.RunShellCommand("content query --uri content://sms/inbox --sort 'date DESC'")
	if err != nil {
		p.log.Warn("could not query sms columns", zap.Error(err))
		return
	}
	parts := reRowSplit.Split(strings.TrimSpace(out), -1)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p.log.Info("sms row columns (for diagnostics)", zap.String("row", part))
		return
	}
}

// ParseSMSOutput parses the raw output of "content query --uri content://sms/inbox".
// Handles multiline message bodies: body spans until the ", date=<digits>" suffix.
// Returns errSQLite if the device reports a SQLiteException (e.g. missing column).
func ParseSMSOutput(raw string) ([]model.Message, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "No result found") {
		return nil, nil
	}
	if strings.Contains(raw, "SQLiteException") || strings.Contains(raw, "Error while accessing provider") {
		return nil, fmt.Errorf("%w: %.120s", errSQLite, raw)
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

	subID := ""
	if subMatch := reSubscriptionID.FindStringSubmatch(row); subMatch != nil {
		subID = subMatch[1]
	}

	return model.Message{
		AndroidID:      idMatch[1],
		SubscriptionID: subID,
		Address:        address,
		Body:           body,
		ReceivedAt:     time.UnixMilli(dateMs),
	}, nil
}
