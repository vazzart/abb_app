package modem

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"abb/config"
	"abb/internal/model"
)

// hellohello from +27838890001 — the classic PDU example from the GSM 03.40 spec tutorials.
const knownPDU = "07917283010010F5040BC87238880900F10000993092516195800AE8329BFD4697D9EC37"

type recorder struct {
	mu   sync.Mutex
	msgs []model.Message
	err  error
}

func (r *recorder) handle(_ context.Context, m model.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.msgs = append(r.msgs, m)
	return nil
}

func (r *recorder) all() []model.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.Message(nil), r.msgs...)
}

func testCfg() config.ModemConfig {
	return config.ModemConfig{
		PollInterval:    time.Hour,
		Storage:         "SM",
		DeleteAfterRead: true,
		Labels:          []config.ModemLabel{{ICCID: "89950123456789012345", Name: "Magti"}},
	}
}

// a7670ePorts is the layout seen on HA OS: option binds interfaces 2 (DIAG), 4 and 5.
var a7670ePorts = []string{"/dev/ttyUSB0", "/dev/ttyUSB1", "/dev/ttyUSB2"}

func startWorker(t *testing.T, f *fakeModem, cfg config.ModemConfig, rec *recorder) *Worker {
	t.Helper()
	cand := Candidate{USBPath: "1-1.2.3.3", Ports: a7670ePorts}
	w := NewWorker(cfg, cand, f.open, rec.handle, zap.NewNop())
	if err := w.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = w.at.Close() })
	return w
}

func TestInit_ReadsModemInfoAndConfiguresSMS(t *testing.T) {
	f := newFakeModem(t)
	w := startWorker(t, f, testCfg(), &recorder{})

	info := w.Info()
	if info.Port != "/dev/ttyUSB2" {
		t.Errorf("port = %q, want ttyUSB2 probed first", info.Port)
	}
	if info.IMEI != "861234567890123" || info.Number != "+995555123456" || info.Operator != "MAGTI" {
		t.Errorf("info = %+v", info)
	}
	if info.Name != "Magti" {
		t.Errorf("name = %q, want label matched by ICCID (trailing F ignored)", info.Name)
	}
	for _, c := range []string{"AT+CMGF=0", `AT+CPMS="SM","SM","SM"`, "AT+CNMI=2,1,0,0,0"} {
		if len(f.sent(c)) != 1 {
			t.Errorf("command %s not sent", c)
		}
	}
}

func TestInit_SkipsPortsThatDoNotAnswer(t *testing.T) {
	f := newFakeModem(t)
	f.dead["/dev/ttyUSB2"] = true
	f.dead["/dev/ttyUSB0"] = true // DIAG
	w := startWorker(t, f, testCfg(), &recorder{})
	if w.Info().Port != "/dev/ttyUSB1" {
		t.Errorf("port = %q, want fallback to ttyUSB1", w.Info().Port)
	}
}

func TestATPortOrder(t *testing.T) {
	cases := map[string]struct{ ports, want []string }{
		"three ports (HA OS)": {a7670ePorts, []string{"/dev/ttyUSB2", "/dev/ttyUSB1", "/dev/ttyUSB0"}},
		"five ports":          {[]string{"0", "1", "2", "3", "4"}, []string{"2", "4", "3", "1", "0"}},
		"two ports":           {[]string{"0", "1"}, []string{"1", "0"}},
	}
	for name, tc := range cases {
		var opened []string
		w := NewWorker(testCfg(), Candidate{Ports: tc.ports}, func(p string) (io.ReadWriteCloser, error) {
			opened = append(opened, p)
			return nil, errors.New("skip")
		}, nil, zap.NewNop())
		_, _, _ = w.findATPort(context.Background())
		if !slices.Equal(opened, tc.want) {
			t.Errorf("%s: probe order = %v, want %v", name, opened, tc.want)
		}
	}
}

func TestInit_NoSIMReportsIMEI(t *testing.T) {
	f := newFakeModem(t)
	f.noSIM = true
	cand := Candidate{USBPath: "1-1.2.3.3", Ports: a7670ePorts}
	w := NewWorker(testCfg(), cand, f.open, (&recorder{}).handle, zap.NewNop())
	// Each clock read jumps an hour ahead, so the SIM wait deadline passes on the first check.
	clock := time.Now()
	w.now = func() time.Time { clock = clock.Add(time.Hour); return clock }

	err := w.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "861234567890123") || !strings.Contains(err.Error(), "SIM not ready") {
		t.Fatalf("err = %v, want SIM not ready with IMEI", err)
	}
}

func TestReadAll_DecodesAndDeletes(t *testing.T) {
	f := newFakeModem(t)
	f.store(0, knownPDU)
	f.store(1, deliverPDUs(t, "+995599000111", "Код подтверждения: 4821")[0])
	long := deliverPDUs(t, "Bank", "Длинное сообщение, которое не помещается в один сегмент UCS2 и поэтому разбивается на несколько частей при отправке.")
	if len(long) < 2 {
		t.Fatalf("expected concatenated message, got %d segments", len(long))
	}
	// Store segments in reverse order to check reassembly ordering.
	for i := range long {
		f.store(10+i, long[len(long)-1-i])
	}

	rec := &recorder{}
	w := startWorker(t, f, testCfg(), rec)
	if err := w.ReadAll(context.Background()); err != nil {
		t.Fatalf("read all: %v", err)
	}

	msgs := rec.all()
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	byAddr := map[string]model.Message{}
	for _, m := range msgs {
		byAddr[m.Address] = m
		if m.Source != "modem" || m.DeviceName != "Magti" || m.SimName != "+995555123456" {
			t.Errorf("metadata = %+v", m)
		}
		if m.ReceivedAt.Location() != time.Local {
			t.Errorf("received_at zone = %v, want local (SCTS zone cannot be stored)", m.ReceivedAt.Location())
		}
	}
	if got := byAddr["27838890001"].Body; got != "hellohello" {
		t.Errorf("known pdu body = %q", got)
	}
	if got := byAddr["+995599000111"].Body; got != "Код подтверждения: 4821" {
		t.Errorf("cyrillic body = %q", got)
	}
	if want := "Длинное сообщение, которое не помещается в один сегмент UCS2 и поэтому разбивается на несколько частей при отправке."; byAddr["Bank"].Body != want {
		t.Errorf("concatenated body = %q", byAddr["Bank"].Body)
	}
	if f.stored() != 0 {
		t.Errorf("%d SMS left on modem, want all deleted", f.stored())
	}
}

func TestReadAll_KeepsSMSWhenHandlerFails(t *testing.T) {
	f := newFakeModem(t)
	f.store(3, knownPDU)
	rec := &recorder{err: errors.New("db locked")}
	w := startWorker(t, f, testCfg(), rec)

	if err := w.ReadAll(context.Background()); err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(f.sent("AT+CMGD")) != 0 || f.stored() != 1 {
		t.Fatal("SMS must stay on the modem when storing failed")
	}
}

func TestReadAll_NoDeleteWhenDisabled(t *testing.T) {
	f := newFakeModem(t)
	f.store(3, knownPDU)
	cfg := testCfg()
	cfg.DeleteAfterRead = false
	rec := &recorder{}
	w := startWorker(t, f, cfg, rec)

	_ = w.ReadAll(context.Background())
	_ = w.ReadAll(context.Background())
	if f.stored() != 1 {
		t.Fatal("SMS must stay on the modem")
	}
	msgs := rec.all()
	if len(msgs) != 2 || msgs[0].AndroidID != msgs[1].AndroidID {
		t.Fatalf("re-read must yield the same dedup key: %+v", msgs)
	}
}

func TestReadAll_IncompleteConcatWaitsThenDelivers(t *testing.T) {
	f := newFakeModem(t)
	parts := deliverPDUs(t, "Bank", "Длинное сообщение, которое не помещается в один сегмент UCS2 и поэтому разбивается на несколько частей при отправке.")
	f.store(5, parts[0]) // second part never arrives

	rec := &recorder{}
	w := startWorker(t, f, testCfg(), rec)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return now }

	_ = w.ReadAll(context.Background())
	if len(rec.all()) != 0 {
		t.Fatal("incomplete message must wait for missing parts")
	}

	now = now.Add(incompleteTimeout + time.Second)
	_ = w.ReadAll(context.Background())
	msgs := rec.all()
	if len(msgs) != 1 {
		t.Fatalf("got %d messages after timeout, want 1", len(msgs))
	}
	if got := []rune(msgs[0].Body); string(got[len(got)-3:]) != "[…]" {
		t.Errorf("partial body should be marked, got %q", msgs[0].Body)
	}
	if f.stored() != 0 {
		t.Error("partial segments must be deleted after delivery")
	}
}

func TestRun_ReadsOnCMTIAndStopsWhenPortCloses(t *testing.T) {
	f := newFakeModem(t)
	rec := &recorder{}
	w := startWorker(t, f, testCfg(), rec)

	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background()) }()

	// Wait for the initial ReadAll, then deliver a new SMS with +CMTI.
	waitFor(t, func() bool { return len(f.sent("AT+CMGL=4")) >= 1 })
	f.store(7, knownPDU)
	f.urc(`+CMTI: "SM",7`)
	waitFor(t, func() bool { return len(rec.all()) == 1 })

	f.mu.Lock()
	_ = f.conn.Close()
	f.mu.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run must return an error when the port closes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after port closed")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestParsers(t *testing.T) {
	if got := quotedField([]string{`+CNUM: "","+995555123456",145`}, "+CNUM:", 1); got != "+995555123456" {
		t.Errorf("CNUM = %q", got)
	}
	if got := quotedField([]string{`+COPS: 0`}, "+COPS:", 0); got != "" {
		t.Errorf("COPS without operator = %q", got)
	}
	if got := parseICCID([]string{"+ICCID: 8995012345"}); got != "8995012345" {
		t.Errorf("ICCID = %q", got)
	}
	if got := firstDigits([]string{"861234567890123"}); got != "861234567890123" {
		t.Errorf("IMEI = %q", got)
	}
	if got := resolveName([]config.ModemLabel{{IMEI: "861234567890123", Name: "Beeline"}}, Info{IMEI: "861234567890123"}); got != "Beeline" {
		t.Errorf("label by IMEI = %q", got)
	}
	if got := resolveName(nil, Info{IMEI: "861234567890123"}); got != "A7670E …0123" {
		t.Errorf("generated name = %q", got)
	}
}
