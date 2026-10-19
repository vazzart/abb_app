package modem

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// scriptedPort answers each command with a fixed raw reply.
func scriptedPort(t *testing.T, replies map[string]string) *ATPort {
	t.Helper()
	host, dev := net.Pipe()
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := dev.Read(buf)
			if err != nil {
				return
			}
			cmd := strings.TrimSpace(string(buf[:n]))
			if r, ok := replies[cmd]; ok {
				_, _ = io.WriteString(dev, r)
			}
		}
	}()
	p := NewATPort(host)
	t.Cleanup(func() { _ = p.Close(); _ = dev.Close() })
	return p
}

func TestCommand_OKWithLines(t *testing.T) {
	p := scriptedPort(t, map[string]string{
		"AT+CGSN": "AT+CGSN\r\n861234567890123\r\n\r\nOK\r\n",
	})
	lines, err := p.Command(context.Background(), "AT+CGSN", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 || lines[0] != "861234567890123" {
		t.Fatalf("lines = %q, want IMEI only (echo stripped)", lines)
	}
}

func TestCommand_Errors(t *testing.T) {
	p := scriptedPort(t, map[string]string{
		"AT+X":    "\r\nERROR\r\n",
		"AT+CMGD": "\r\n+CMS ERROR: invalid memory index\r\n",
		"AT+CPIN": "\r\n+CME ERROR: SIM not inserted\r\n",
	})
	for _, cmd := range []string{"AT+X", "AT+CMGD", "AT+CPIN"} {
		if _, err := p.Command(context.Background(), cmd, time.Second); err == nil {
			t.Errorf("%s: expected error", cmd)
		}
	}
}

func TestCommand_URCInsideResponseIsSeparated(t *testing.T) {
	p := scriptedPort(t, map[string]string{
		"AT+COPS?": "\r\n+COPS: 0,0,\"MAGTI\",7\r\n\r\n+CMTI: \"SM\",4\r\n\r\nOK\r\n",
	})
	lines, err := p.Command(context.Background(), "AT+COPS?", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "+COPS:") {
		t.Fatalf("lines = %q", lines)
	}
	select {
	case u := <-p.URC():
		if u != `+CMTI: "SM",4` {
			t.Fatalf("urc = %q", u)
		}
	case <-time.After(time.Second):
		t.Fatal("URC not delivered")
	}
}

func TestCommand_Timeout(t *testing.T) {
	p := scriptedPort(t, nil)
	_, err := p.Command(context.Background(), "AT", 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestCommand_PortClosed(t *testing.T) {
	host, dev := net.Pipe()
	p := NewATPort(host)
	_ = dev.Close()
	<-p.Done()
	if _, err := p.Command(context.Background(), "AT", time.Second); !errors.Is(err, ErrPortClosed) {
		t.Fatalf("expected ErrPortClosed, got %v", err)
	}
}
