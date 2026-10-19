package modem

import (
	"testing"
	"time"

	"go.uber.org/zap"

	"abb/config"
)

func TestBackoff_DoublesUpToCap(t *testing.T) {
	m := NewManager(config.ModemConfig{WatchInterval: 10 * time.Second}, nil, zap.NewNop())
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }

	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second,
		160 * time.Second, maxRetryDelay, maxRetryDelay}
	for i, w := range want {
		if got := m.backoff("1-1.2"); got != w {
			t.Fatalf("attempt %d: delay = %s, want %s", i+1, got, w)
		}
	}
	if !m.retryAt["1-1.2"].Equal(now.Add(maxRetryDelay)) {
		t.Errorf("retryAt = %v", m.retryAt["1-1.2"])
	}
}
