package dispatcher_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"abb/internal/db"
	"abb/internal/dispatcher"
	"abb/internal/sender"
)

// --- T4-02: exponential backoff function --------------------------------

func TestBackoff_Values(t *testing.T) {
	base := time.Minute
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, 1 * time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 16 * time.Minute},
	}
	for _, tc := range cases {
		got := dispatcher.Backoff(tc.attempts, base)
		if got != tc.want {
			t.Errorf("Backoff(%d, minute) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

// T4-02: каждый следующий интервал строго больше предыдущего
func TestBackoff_StrictlyIncreasing(t *testing.T) {
	base := time.Second
	prev := dispatcher.Backoff(1, base)
	for i := 2; i <= 10; i++ {
		curr := dispatcher.Backoff(i, base)
		if curr <= prev {
			t.Errorf("attempt %d: backoff not growing: %v <= %v", i, curr, prev)
		}
		prev = curr
	}
}

// T4-02: backoff не превышает 1 час
func TestBackoff_CappedAtOneHour(t *testing.T) {
	for _, attempts := range []int{20, 50, 100} {
		got := dispatcher.Backoff(attempts, time.Minute)
		if got > time.Hour {
			t.Errorf("Backoff(%d) = %v, expected <= 1h", attempts, got)
		}
	}
}

// T4-02: base=0 → всегда 0 (моментальный retry в тестах)
func TestBackoff_ZeroBase(t *testing.T) {
	for _, attempts := range []int{1, 3, 10} {
		got := dispatcher.Backoff(attempts, 0)
		if got != 0 {
			t.Errorf("Backoff(%d, 0) = %v, want 0", attempts, got)
		}
	}
}

// --- helpers (retry-specific) -------------------------------------------

func seedFailed(t *testing.T, database *db.DB, androidID, channel string, attempts int) (msgID, outboxID int64) {
	t.Helper()
	ctx := context.Background()
	msgID, outboxID = seedMessage(t, database, androidID, "+7900", "body", channel)
	for i := range attempts {
		database.MarkOutboxFailed(ctx, outboxID, "simulated error")         //nolint:errcheck
		database.AppendDeliveryLog(ctx, outboxID, false, "simulated error") //nolint:errcheck
		if i < attempts-1 {
			database.ResetToPending(ctx, outboxID) //nolint:errcheck
		}
	}
	return msgID, outboxID
}

func newRetryWorker(database *db.DB, disp *dispatcher.Dispatcher, maxAttempts int, base time.Duration) *dispatcher.RetryWorker {
	return dispatcher.NewRetryWorker(database, disp, maxAttempts, base, time.Hour, noopLogger())
}

// cancelAfter returns a context that cancels itself after d.
func cancelAfter(parent context.Context, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(parent, d)
	t := parent.Value(testingTKey{})
	if tt, ok := t.(*testing.T); ok {
		tt.Cleanup(cancel)
	} else {
		// No *testing.T available; context self-cancels via timeout, so timer
		// resources are released automatically — explicit cancel is optional here.
		_ = cancel
	}
	return ctx
}

// testingTKey is the context key used to propagate *testing.T into cancelAfter.
type testingTKey struct{}

// --- T4-01: retry после восстановления ----------------------------------

func TestRetryWorker_RetriesFailedItem(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	seedFailed(t, database, "1", "telegram", 1)

	mock := newMock("telegram", nil)
	disp := dispatcher.New(database, map[string]sender.Sender{"telegram": mock}, time.Hour, 0, noopLogger())

	// base=0 → immediate retry
	rw := newRetryWorker(database, disp, 5, 0)
	rw.Run(cancelAfter(ctx, 10*time.Millisecond))

	if len(mock.Calls()) != 1 {
		t.Errorf("expected 1 delivery call after retry, got %d", len(mock.Calls()))
	}
	sent, _ := database.CountOutbox(ctx, "sent")
	if sent != 1 {
		t.Errorf("expected 1 sent item, got %d", sent)
	}
}

// T4-01: item остаётся failed если backoff ещё не истёк
func TestRetryWorker_RespectBackoff(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	seedFailed(t, database, "1", "telegram", 1)

	mock := newMock("telegram", nil)
	disp := dispatcher.New(database, map[string]sender.Sender{"telegram": mock}, time.Hour, 0, noopLogger())

	// base=1h → backoff не истёк
	rw := newRetryWorker(database, disp, 5, time.Hour)
	rw.Run(cancelAfter(ctx, 10*time.Millisecond))

	if len(mock.Calls()) != 0 {
		t.Errorf("expected 0 calls when backoff not elapsed, got %d", len(mock.Calls()))
	}
	failed, _ := database.CountOutbox(ctx, "failed")
	if failed != 1 {
		t.Errorf("item should still be failed, got %d failed items", failed)
	}
}

// --- T4-03: исчерпание попыток ------------------------------------------

func TestRetryWorker_MaxAttemptsExceeded(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	const maxAttempts = 3
	seedFailed(t, database, "1", "telegram", maxAttempts) // attempts == max

	mock := newMock("telegram", nil)
	disp := dispatcher.New(database, map[string]sender.Sender{"telegram": mock}, time.Hour, 0, noopLogger())
	rw := newRetryWorker(database, disp, maxAttempts, 0)
	rw.Run(cancelAfter(ctx, 10*time.Millisecond))

	if len(mock.Calls()) != 0 {
		t.Errorf("exhausted item must not be retried, got %d calls", len(mock.Calls()))
	}
	failed, _ := database.CountOutbox(ctx, "failed")
	if failed != 1 {
		t.Errorf("exhausted item must remain failed, got %d failed", failed)
	}
}

// --- T4-04: delivery_log содержит все попытки --------------------------

func TestRetryWorker_DeliveryLogAllAttempts(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	_, outboxID := seedMessage(t, database, "1", "+7900", "body", "telegram")

	// Попытка 1 — fail
	failMock := newMock("telegram", errors.New("network error"))
	dispatcher.New(database, map[string]sender.Sender{"telegram": failMock}, time.Hour, 0, noopLogger()).
		DispatchOnce(ctx)

	// RetryWorker сбрасывает в pending (base=0)
	okMock := newMock("telegram", nil)
	disp := dispatcher.New(database, map[string]sender.Sender{"telegram": okMock}, time.Hour, 0, noopLogger())
	newRetryWorker(database, disp, 5, 0).Run(cancelAfter(ctx, 10*time.Millisecond))

	// Всего 2 записи: 1 failure + 1 success
	n, _ := database.CountDeliveryLog(ctx, outboxID)
	if n != 2 {
		t.Errorf("delivery_log: expected 2 entries (fail+success), got %d", n)
	}
}

// --- T4-05: sent-записи не трогаются ------------------------------------

func TestRetryWorker_DoesNotRetouchSentItems(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	_, outboxID := seedMessage(t, database, "1", "+7900", "body", "telegram")
	database.MarkOutboxSent(ctx, outboxID) //nolint:errcheck

	mock := newMock("telegram", nil)
	disp := dispatcher.New(database, map[string]sender.Sender{"telegram": mock}, time.Hour, 0, noopLogger())
	rw := newRetryWorker(database, disp, 5, 0)
	rw.Run(cancelAfter(ctx, 10*time.Millisecond))

	if len(mock.Calls()) != 0 {
		t.Errorf("sent item must not be retried, got %d calls", len(mock.Calls()))
	}
}

// --- T4-06: graceful shutdown -------------------------------------------

func TestRetryWorker_GracefulShutdown(t *testing.T) {
	database := openTestDB(t)

	mock := newMock("telegram", nil)
	disp := dispatcher.New(database, map[string]sender.Sender{"telegram": mock}, time.Hour, 0, noopLogger())
	rw := dispatcher.NewRetryWorker(database, disp, 5, time.Hour, 50*time.Millisecond, noopLogger())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		rw.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// graceful stop
	case <-time.After(500 * time.Millisecond):
		t.Error("RetryWorker did not stop within 500ms after context cancel")
	}
}

// --- T4-03: полный сценарий: fail × N → навсегда failed ----------------

func TestRetryWorker_FullExhaustionScenario(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	const maxAttempts = 3
	_, outboxID := seedMessage(t, database, "1", "+7900", "body", "telegram")

	failMock := newMock("telegram", errors.New("always fails"))
	disp := dispatcher.New(database, map[string]sender.Sender{"telegram": failMock}, time.Hour, 0, noopLogger())
	rw := newRetryWorker(database, disp, maxAttempts, 0)

	for range maxAttempts {
		disp.DispatchOnce(ctx)
		rw.Run(cancelAfter(ctx, 10*time.Millisecond))
	}

	failed, _ := database.CountOutbox(ctx, "failed")
	exhausted, _ := database.CountExhaustedOutbox(ctx, maxAttempts)
	if failed != 1 || exhausted != 1 {
		t.Errorf("failed=%d exhausted=%d, both want 1", failed, exhausted)
	}

	n, _ := database.CountDeliveryLog(ctx, outboxID)
	if n < maxAttempts {
		t.Errorf("delivery_log: expected >= %d entries, got %d", maxAttempts, n)
	}
}
