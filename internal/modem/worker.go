package modem

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/pdumode"
	"github.com/warthog618/sms/encoding/tpdu"
	"go.uber.org/zap"

	"abb/config"
	"abb/internal/model"
)

const (
	probeTimeout   = 2 * time.Second
	commandTimeout = 10 * time.Second
	listTimeout    = 30 * time.Second
	simReadyWait   = 30 * time.Second

	// incompleteTimeout is how long parts of a concatenated SMS wait for the
	// missing segments before being delivered as they are.
	incompleteTimeout = 10 * time.Minute
)

// Handler stores a received message. The SMS is deleted from the modem only
// after the handler returns nil.
type Handler func(ctx context.Context, msg model.Message) error

// OpenFunc opens a serial port by device path.
type OpenFunc func(path string) (io.ReadWriteCloser, error)

// Info describes an initialised modem.
type Info struct {
	USBPath  string
	Port     string
	IMEI     string
	ICCID    string
	Number   string
	Operator string
	Name     string // label from config or a generated name
}

// SimName is the second part of the "To:" line: phone number, else operator.
func (i Info) SimName() string {
	if i.Number != "" {
		return i.Number
	}
	return i.Operator
}

// Worker reads SMS from one modem until the port fails or ctx is cancelled.
type Worker struct {
	cfg     config.ModemConfig
	cand    Candidate
	open    OpenFunc
	handler Handler
	log     *zap.Logger

	at        *ATPort
	info      Info
	firstSeen map[string]time.Time // concatenated-SMS key -> first time a part was seen
	badPDU    map[int]bool         // storage indexes already reported as undecodable
	now       func() time.Time
}

func NewWorker(cfg config.ModemConfig, cand Candidate, open OpenFunc, handler Handler, log *zap.Logger) *Worker {
	return &Worker{
		cfg:       cfg,
		cand:      cand,
		open:      open,
		handler:   handler,
		log:       log.With(zap.String("usb", cand.USBPath)),
		firstSeen: make(map[string]time.Time),
		badPDU:    make(map[int]bool),
		now:       time.Now,
	}
}

// Info returns modem details; valid after Init succeeded.
func (w *Worker) Info() Info { return w.info }

// Init finds the AT port and configures the modem for PDU-mode SMS with +CMTI notifications.
func (w *Worker) Init(ctx context.Context) error {
	port, at, err := w.findATPort(ctx)
	if err != nil {
		return err
	}
	w.at = at
	w.info.USBPath = w.cand.USBPath
	w.info.Port = port

	if err := w.setup(ctx); err != nil {
		_ = at.Close()
		return err
	}
	return nil
}

// Run reads all stored SMS, then waits for +CMTI notifications, re-reading
// every poll interval as a fallback. Returns when the port fails or ctx ends.
func (w *Worker) Run(ctx context.Context) error {
	defer func() { _ = w.at.Close() }()

	interval := w.cfg.PollInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if err := w.ReadAll(ctx); err != nil && !errors.Is(err, ErrPortClosed) {
		w.log.Warn("read sms failed", zap.Error(err))
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.at.Done():
			return fmt.Errorf("port %s: %w", w.info.Port, w.at.Err())
		case line := <-w.at.URC():
			if !strings.HasPrefix(line, "+CMTI:") {
				w.log.Debug("urc", zap.String("line", line))
				continue
			}
		case <-ticker.C:
		}
		if err := w.ReadAll(ctx); err != nil {
			if errors.Is(err, ErrPortClosed) {
				return fmt.Errorf("port %s: %w", w.info.Port, err)
			}
			w.log.Warn("read sms failed", zap.Error(err))
		}
	}
}

// findATPort probes the candidate's ports with "AT" and returns the first that answers OK.
// The third port (ttyUSB2 on a single A7670E) is tried first as it is the documented AT port,
// then the rest from the highest interface down: the lowest one is usually DIAG, which never
// answers AT and would only cost a probe timeout.
func (w *Worker) findATPort(ctx context.Context) (string, *ATPort, error) {
	order := slices.Clone(w.cand.Ports)
	slices.Reverse(order)
	if n := len(order); n > 2 {
		// order is reversed: the third port is at n-3.
		order = slices.Concat(order[n-3:n-2], order[:n-3], order[n-2:])
	}

	for _, path := range order {
		rw, err := w.open(path)
		if err != nil {
			w.log.Debug("open port failed", zap.String("port", path), zap.Error(err))
			continue
		}
		at := NewATPort(rw)
		for range 2 {
			if _, err = at.Command(ctx, "AT", probeTimeout); err == nil {
				return path, at, nil
			}
		}
		_ = at.Close()
	}
	return "", nil, fmt.Errorf("no port of %s answers AT (ports: %v)", w.cand.USBPath, w.cand.Ports)
}

func (w *Worker) setup(ctx context.Context) error {
	w.cmd(ctx, "ATE0")
	w.cmd(ctx, "AT+CMEE=2")

	// IMEI does not need a SIM; read it first so "SIM not ready" says which modem it is.
	if lines, err := w.cmd(ctx, "AT+CGSN"); err == nil {
		w.info.IMEI = firstDigits(lines)
	}
	if err := w.waitSIM(ctx); err != nil {
		return fmt.Errorf("modem IMEI %s: %w", w.info.IMEI, err)
	}
	if lines, err := w.cmd(ctx, "AT+CICCID"); err == nil {
		w.info.ICCID = parseICCID(lines)
	}
	if lines, err := w.cmd(ctx, "AT+CNUM"); err == nil {
		w.info.Number = quotedField(lines, "+CNUM:", 1)
	}
	// Report the operator as a name ("Tele2") rather than the numeric MCC/MNC ("25020").
	w.cmd(ctx, "AT+COPS=3,0")
	if lines, err := w.cmd(ctx, "AT+COPS?"); err == nil {
		w.info.Operator = quotedField(lines, "+COPS:", 0)
	}
	w.info.Name = resolveName(w.cfg.Labels, w.info)

	storage := w.cfg.Storage
	if storage == "" {
		storage = "SM"
	}
	for _, c := range []string{
		"AT+CMGF=0",
		fmt.Sprintf(`AT+CPMS="%s","%s","%s"`, storage, storage, storage),
		"AT+CNMI=2,1,0,0,0",
	} {
		if _, err := w.cmd(ctx, c); err != nil {
			return err
		}
	}
	w.log = w.log.With(zap.String("modem", w.info.Name), zap.String("imei", w.info.IMEI))
	return nil
}

// waitSIM polls AT+CPIN? quietly (the SIM may still be initialising after power-up);
// the caller logs the final error once.
func (w *Worker) waitSIM(ctx context.Context) error {
	deadline := w.now().Add(simReadyWait)
	for {
		lines, err := w.at.Command(ctx, "AT+CPIN?", commandTimeout)
		if err == nil && len(lines) > 0 && strings.Contains(lines[0], "READY") {
			return nil
		}
		if w.now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("%s", strings.Join(lines, "; "))
			}
			return fmt.Errorf("SIM not ready: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (w *Worker) cmd(ctx context.Context, c string) ([]string, error) {
	lines, err := w.at.Command(ctx, c, commandTimeout)
	if err != nil {
		w.log.Warn("at command failed", zap.String("cmd", c), zap.Error(err))
	}
	return lines, err
}

// storedPDU is one SMS segment from AT+CMGL.
type storedPDU struct {
	index int
	tpdu  *tpdu.TPDU
}

// group is a complete or partial SMS made of one or more segments.
type group struct {
	key      string
	segments []storedPDU
	total    int
}

var reCMGL = regexp.MustCompile(`^\+CMGL:\s*(\d+),`)

// ReadAll lists every stored SMS, hands complete messages to the handler and
// deletes the ones that were stored successfully.
func (w *Worker) ReadAll(ctx context.Context) error {
	lines, err := w.at.Command(ctx, "AT+CMGL=4", listTimeout)
	if err != nil {
		return err
	}

	groups := make(map[string]*group)
	var order []string
	var orphans []int // non-DELIVER PDUs (e.g. status reports) to drop

	for i := 0; i < len(lines); i++ {
		m := reCMGL.FindStringSubmatch(lines[i])
		if m == nil || i+1 >= len(lines) {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		i++
		t, err := decodePDU(lines[i])
		if err != nil {
			if !w.badPDU[idx] {
				w.badPDU[idx] = true
				w.log.Warn("undecodable sms pdu, leaving it on the modem",
					zap.Int("index", idx), zap.String("pdu", lines[i]), zap.Error(err))
			}
			continue
		}
		if t.SmsType() != tpdu.SmsDeliver {
			orphans = append(orphans, idx)
			continue
		}

		key := fmt.Sprintf("single:%d", idx)
		total := 1
		if segs, _, mref, ok := t.ConcatInfo(); ok && segs > 1 {
			key = fmt.Sprintf("concat:%s:%d:%d", t.OA.Number(), mref, segs)
			total = segs
		}
		g := groups[key]
		if g == nil {
			g = &group{key: key, total: total}
			groups[key] = g
			order = append(order, key)
		}
		g.segments = append(g.segments, storedPDU{index: idx, tpdu: t})
	}

	for _, key := range order {
		g := groups[key]
		complete := len(g.segments) >= g.total
		if !complete {
			first, seen := w.firstSeen[key]
			if !seen {
				w.firstSeen[key] = w.now()
				continue
			}
			if w.now().Sub(first) < incompleteTimeout {
				continue
			}
			w.log.Warn("delivering incomplete concatenated sms",
				zap.Int("have", len(g.segments)), zap.Int("total", g.total))
		}

		msg, err := w.buildMessage(g, complete)
		if err != nil {
			w.log.Warn("decode sms text failed", zap.Error(err))
			continue
		}
		if err := w.handler(ctx, msg); err != nil {
			w.log.Error("store sms failed, keeping it on the modem", zap.Error(err))
			continue
		}
		delete(w.firstSeen, key)
		w.deleteSegments(ctx, g.segments)
	}

	if w.cfg.DeleteAfterRead {
		for _, idx := range orphans {
			_, _ = w.cmd(ctx, fmt.Sprintf("AT+CMGD=%d", idx))
		}
	}
	return nil
}

func (w *Worker) deleteSegments(ctx context.Context, segs []storedPDU) {
	if !w.cfg.DeleteAfterRead {
		return
	}
	for _, s := range segs {
		if _, err := w.cmd(ctx, fmt.Sprintf("AT+CMGD=%d", s.index)); err == nil {
			delete(w.badPDU, s.index)
		}
	}
}

func (w *Worker) buildMessage(g *group, complete bool) (model.Message, error) {
	sort.Slice(g.segments, func(i, j int) bool {
		_, si, _, _ := g.segments[i].tpdu.ConcatInfo()
		_, sj, _, _ := g.segments[j].tpdu.ConcatInfo()
		return si < sj
	})
	tpdus := make([]*tpdu.TPDU, len(g.segments))
	for i, s := range g.segments {
		tpdus[i] = s.tpdu
	}
	text, err := sms.Decode(tpdus)
	if err != nil {
		return model.Message{}, err
	}
	body := string(text)
	if !complete {
		body += " […]"
	}

	first := tpdus[0]
	sender := first.OA.Number()
	received := first.SCTS.Time
	if received.IsZero() {
		received = w.now()
	}

	return model.Message{
		AndroidID:  messageKey(w.info.IMEI, sender, received, body),
		Address:    sender,
		Body:       body,
		DeviceName: w.info.Name,
		SimName:    w.info.SimName(),
		Source:     "modem",
		ReceivedAt: received,
	}, nil
}

// decodePDU parses a PDU-mode hex line (SMSC address + TPDU) as an MT TPDU.
func decodePDU(hexLine string) (*tpdu.TPDU, error) {
	p, err := pdumode.UnmarshalHexString(strings.TrimSpace(hexLine))
	if err != nil {
		return nil, err
	}
	return sms.Unmarshal(p.TPDU)
}

// messageKey builds the dedup key stored in messages.android_id. It is stable
// across restarts, so an SMS saved but not yet deleted is not delivered twice.
func messageKey(imei, sender string, received time.Time, body string) string {
	h := sha1.Sum([]byte(sender + "|" + strconv.FormatInt(received.Unix(), 10) + "|" + body))
	return "modem:" + imei + ":" + hex.EncodeToString(h[:8])
}

// resolveName returns the configured label for the modem, matched by IMEI or ICCID,
// or a generated "A7670E …1234" name.
func resolveName(labels []config.ModemLabel, info Info) string {
	for _, l := range labels {
		if l.Name == "" {
			continue
		}
		if l.IMEI != "" && strings.TrimSpace(l.IMEI) == info.IMEI {
			return l.Name
		}
		if l.ICCID != "" && normICCID(l.ICCID) == normICCID(info.ICCID) {
			return l.Name
		}
	}
	if len(info.IMEI) >= 4 {
		return "A7670E …" + info.IMEI[len(info.IMEI)-4:]
	}
	return "A7670E " + info.USBPath
}

func normICCID(s string) string {
	return strings.TrimRight(strings.ToUpper(strings.TrimSpace(s)), "F")
}

// firstDigits returns the first response line that is all digits (AT+CGSN output).
func firstDigits(lines []string) string {
	for _, l := range lines {
		l = strings.TrimSpace(strings.TrimPrefix(l, "+CGSN:"))
		if l != "" && strings.Trim(l, "0123456789") == "" {
			return l
		}
	}
	return ""
}

// parseICCID handles "+ICCID: 8995...", "+CICCID: ..." and bare ICCID lines.
func parseICCID(lines []string) string {
	for _, l := range lines {
		if i := strings.Index(l, ":"); i >= 0 {
			l = l[i+1:]
		}
		l = strings.Trim(strings.TrimSpace(l), `"`)
		if l != "" {
			return l
		}
	}
	return ""
}

// quotedField returns the n-th double-quoted value of the first line with prefix,
// e.g. quotedField([`+CNUM: "","+995555123456",145`], "+CNUM:", 1) = "+995555123456".
func quotedField(lines []string, prefix string, n int) string {
	for _, l := range lines {
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		parts := strings.Split(l, `"`)
		// parts: [before, q0, between, q1, ...]
		if i := 2*n + 1; i < len(parts) {
			return parts[i]
		}
		return ""
	}
	return ""
}
