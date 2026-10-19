package modem

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/pdumode"
	"github.com/warthog618/sms/encoding/tpdu"
)

// fakeModem emulates the AT interface of an A7670E over net.Pipe.
type fakeModem struct {
	t *testing.T

	mu       sync.Mutex
	conn     net.Conn
	imei     string
	iccid    string
	number   string
	storage  map[int]string // index -> PDU hex (SMSC + TPDU)
	commands []string
	dead     map[string]bool // ports that never answer (e.g. DIAG)
	noSIM    bool
}

func newFakeModem(t *testing.T) *fakeModem {
	return &fakeModem{
		t:       t,
		imei:    "861234567890123",
		iccid:   "89950123456789012345F",
		number:  "+995555123456",
		storage: make(map[int]string),
		dead:    make(map[string]bool),
	}
}

// open returns the host side of a new pipe; the modem side is served in a goroutine.
func (f *fakeModem) open(path string) (io.ReadWriteCloser, error) {
	host, dev := net.Pipe()
	f.mu.Lock()
	dead := f.dead[path]
	if !dead {
		f.conn = dev
	}
	f.mu.Unlock()
	go f.serve(dev, dead)
	return host, nil
}

func (f *fakeModem) serve(conn net.Conn, dead bool) {
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\r')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		if cmd == "" {
			continue
		}
		if dead {
			continue
		}
		f.mu.Lock()
		f.commands = append(f.commands, cmd)
		f.mu.Unlock()
		for _, l := range f.respond(cmd) {
			if _, err := io.WriteString(conn, "\r\n"+l+"\r\n"); err != nil {
				return
			}
		}
	}
}

func (f *fakeModem) respond(cmd string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case cmd == "AT+CPIN?":
		if f.noSIM {
			return []string{"+CME ERROR: SIM not inserted"}
		}
		return []string{"+CPIN: READY", "OK"}
	case cmd == "AT+CGSN":
		return []string{f.imei, "OK"}
	case cmd == "AT+CICCID":
		return []string{"+ICCID: " + f.iccid, "OK"}
	case cmd == "AT+CNUM":
		return []string{fmt.Sprintf(`+CNUM: "","%s",145`, f.number), "OK"}
	case cmd == "AT+COPS?":
		return []string{`+COPS: 0,0,"MAGTI",7`, "OK"}
	case cmd == "AT+CMGL=4":
		idx := make([]int, 0, len(f.storage))
		for i := range f.storage {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		var out []string
		for _, i := range idx {
			pdu := f.storage[i]
			out = append(out, fmt.Sprintf("+CMGL: %d,1,,%d", i, len(pdu)/2-1), pdu)
		}
		return append(out, "OK")
	case strings.HasPrefix(cmd, "AT+CMGD="):
		var i int
		_, _ = fmt.Sscanf(cmd, "AT+CMGD=%d", &i)
		delete(f.storage, i)
		return []string{"OK"}
	default:
		return []string{"OK"}
	}
}

// urc pushes an unsolicited line to the host.
func (f *fakeModem) urc(line string) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	_, _ = io.WriteString(conn, "\r\n"+line+"\r\n")
}

func (f *fakeModem) store(idx int, pdu string) {
	f.mu.Lock()
	f.storage[idx] = pdu
	f.mu.Unlock()
}

func (f *fakeModem) stored() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.storage)
}

func (f *fakeModem) sent(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.commands {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// deliverPDUs encodes text as SMS-DELIVER PDU-mode hex strings (one per segment).
func deliverPDUs(t *testing.T, from, text string) []string {
	t.Helper()
	oa := sms.From(from)
	if strings.Trim(from, "+0123456789") != "" {
		// Alphanumeric sender such as a bank name (TON=alphanumeric, TOA=0xD0).
		oa = sms.WithTemplateOption(tpdu.WithOA(tpdu.Address{TOA: 0xD0, Addr: from}))
	}
	tpdus, err := sms.Encode([]byte(text), sms.AsDeliver, oa)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out []string
	for _, tp := range tpdus {
		b, err := tp.MarshalBinary()
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		p := pdumode.PDU{TPDU: b}
		s, err := p.MarshalHexString()
		if err != nil {
			t.Fatalf("pdu hex: %v", err)
		}
		out = append(out, s)
	}
	return out
}
