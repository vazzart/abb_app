package dispatcher

import (
	"context"
	"time"

	"go.uber.org/zap"

	"abb/internal/db"
)

const maxBackoff = time.Hour

// RetryWorker periodically reschedules failed outbox items whose exponential
// backoff period has elapsed, then triggers the Dispatcher to deliver them.
type RetryWorker struct {
	db            *db.DB
	disp          *Dispatcher
	maxAttempts   int
	baseInterval  time.Duration
	checkInterval time.Duration
	log           *zap.Logger
}

func NewRetryWorker(
	database *db.DB,
	disp *Dispatcher,
	maxAttempts int,
	baseInterval time.Duration,
	checkInterval time.Duration,
	log *zap.Logger,
) *RetryWorker {
	return &RetryWorker{
		db:            database,
		disp:          disp,
		maxAttempts:   maxAttempts,
		baseInterval:  baseInterval,
		checkInterval: checkInterval,
		log:           log,
	}
}

// Run checks for retryable items on startup and then on every checkInterval tick.
func (rw *RetryWorker) Run(ctx context.Context) {
	rw.work(ctx)

	ticker := time.NewTicker(rw.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rw.work(ctx)
		}
	}
}

func (rw *RetryWorker) work(ctx context.Context) {
	items, err := rw.db.GetFailedOutbox(ctx, rw.maxAttempts)
	if err != nil {
		rw.log.Error("get failed outbox", zap.Error(err))
		return
	}

	now := time.Now()
	rescheduled := 0
	for _, item := range items {
		due := item.LastAttemptAt.Add(Backoff(item.Attempts, rw.baseInterval))
		if now.Before(due) {
			continue
		}
		if err := rw.db.ResetToPending(ctx, item.ID); err != nil {
			rw.log.Error("reset to pending", zap.Error(err), zap.Int64("outbox_id", item.ID))
			continue
		}
		rescheduled++
		rw.log.Info("retry rescheduled",
			zap.Int64("outbox_id", item.ID),
			zap.Int("attempts", item.Attempts),
			zap.Time("last_attempt", item.LastAttemptAt),
			zap.Duration("backoff", Backoff(item.Attempts, rw.baseInterval)),
		)
	}

	if rescheduled > 0 {
		rw.disp.DispatchOnce(ctx)
	}

	// Warn about permanently failed items so the operator knows to investigate.
	if exhausted, err := rw.db.CountExhaustedOutbox(ctx, rw.maxAttempts); err == nil && exhausted > 0 {
		rw.log.Warn("outbox items exhausted all retry attempts — manual intervention required",
			zap.Int("count", exhausted),
			zap.Int("max_attempts", rw.maxAttempts),
		)
	}
}

// Backoff returns the wait duration before the next retry for the given attempt count.
// Uses exponential backoff: base × 2^(attempts-1), capped at one hour.
// Exported for testing.
func Backoff(attempts int, base time.Duration) time.Duration {
	if attempts <= 1 {
		return base
	}
	d := base
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}
