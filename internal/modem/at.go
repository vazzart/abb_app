package modem

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ErrPortClosed is returned by Command once the underlying port has failed or been closed.
var ErrPortClosed = errors.New("at port closed")

// urcPrefixes lists unsolicited result codes that may arrive in the middle of a
// command response and must not be mistaken for part of it.
var urcPrefixes = []string{
	"+CMTI:", "+CDSI:", "+CMT:", "+CDS:", "RING", "+CLIP:", "+CGEV:",
	"SMS DONE", "PB DONE", "*ATREADY", "+CPIN: NOT READY", "NO CARRIER",
}

func isURC(line string) bool {
	for _, p := range urcPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// ATPort sends AT commands over a serial line and separates command responses
// from unsolicited result codes (URCs).
type ATPort struct {
	rw io.ReadWriteCloser

	cmdMu sync.Mutex // one command at a time

	stateMu sync.Mutex
	waiting bool // a command is waiting for its response

	resp chan string
	urc  chan string
	done chan struct{}
	err  error
}

// NewATPort wraps rw and starts the background line reader.
func NewATPort(rw io.ReadWriteCloser) *ATPort {
	p := &ATPort{
		rw:   rw,
		resp: make(chan string, 256),
		urc:  make(chan string, 32),
		done: make(chan struct{}),
	}
	go p.readLoop()
	return p
}

// URC delivers unsolicited lines (e.g. "+CMTI: \"SM\",3"). Lines are dropped if nobody reads.
func (p *ATPort) URC() <-chan string { return p.urc }

// Done is closed when the port fails or is closed.
func (p *ATPort) Done() <-chan struct{} { return p.done }

// Err returns the error that stopped the reader, if any.
func (p *ATPort) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}

// Close closes the underlying port; the reader stops and Done is closed.
func (p *ATPort) Close() error { return p.rw.Close() }

func (p *ATPort) readLoop() {
	r := bufio.NewReader(p.rw)
	for {
		raw, err := r.ReadString('\n')
		if line := strings.TrimSpace(raw); line != "" {
			p.dispatch(line)
		}
		if err != nil {
			p.err = err
			close(p.done)
			return
		}
	}
}

func (p *ATPort) dispatch(line string) {
	p.stateMu.Lock()
	waiting := p.waiting
	p.stateMu.Unlock()

	if waiting && !isURC(line) {
		select {
		case p.resp <- line:
		case <-time.After(time.Second):
		}
		return
	}
	select {
	case p.urc <- line:
	default:
	}
}

// Command sends cmd and returns the response lines without the echo and the
// final result code. "ERROR", "+CME ERROR" and "+CMS ERROR" are returned as errors.
func (p *ATPort) Command(ctx context.Context, cmd string, timeout time.Duration) ([]string, error) {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()

	select {
	case <-p.done:
		return nil, ErrPortClosed
	default:
	}

	// Drop leftovers of a previous command that timed out.
	for len(p.resp) > 0 {
		<-p.resp
	}

	p.setWaiting(true)
	defer p.setWaiting(false)

	if _, err := io.WriteString(p.rw, cmd+"\r"); err != nil {
		return nil, fmt.Errorf("write %q: %w", cmd, err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var lines []string
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.done:
			return nil, ErrPortClosed
		case <-timer.C:
			return nil, fmt.Errorf("%s: timeout after %s", cmd, timeout)
		case line := <-p.resp:
			switch {
			case line == cmd:
				// echo (before ATE0)
			case line == "OK":
				return lines, nil
			case line == "ERROR",
				strings.HasPrefix(line, "+CME ERROR"),
				strings.HasPrefix(line, "+CMS ERROR"):
				return lines, fmt.Errorf("%s: %s", cmd, line)
			default:
				lines = append(lines, line)
			}
		}
	}
}

func (p *ATPort) setWaiting(v bool) {
	p.stateMu.Lock()
	p.waiting = v
	p.stateMu.Unlock()
}
