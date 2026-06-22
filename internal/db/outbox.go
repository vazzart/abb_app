package db

import (
	"context"
	"time"

	"abb/internal/model"
)

// CreateOutboxEntry adds a pending delivery task for messageID on the given channel.
func (d *DB) CreateOutboxEntry(ctx context.Context, messageID int64, channel string) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO outbox (message_id, channel) VALUES (?, ?)`,
		messageID, channel,
	)
	return err
}

// CountOutbox returns number of outbox rows matching status.
func (d *DB) CountOutbox(ctx context.Context, status string) (int, error) {
	var n int
	err := d.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE status = ?`, status,
	).Scan(&n)
	return n, err
}

// GetPendingOutbox returns all outbox items with status 'pending', oldest first.
func (d *DB) GetPendingOutbox(ctx context.Context) ([]model.OutboxItem, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT id, message_id, channel, status, attempts,
		       COALESCE(last_error, ''), scheduled_at
		FROM outbox
		WHERE status = 'pending'
		ORDER BY scheduled_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.OutboxItem
	for rows.Next() {
		var it model.OutboxItem
		if err := rows.Scan(&it.ID, &it.MessageID, &it.Channel, &it.Status,
			&it.Attempts, &it.LastError, &it.ScheduledAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// UpdateOutboxStatus sets status and last_error for an outbox row.
func (d *DB) UpdateOutboxStatus(ctx context.Context, id int64, status, lastError string) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE outbox SET status = ?, last_error = ? WHERE id = ?`,
		status, lastError, id,
	)
	return err
}

// MarkOutboxSent marks the item as delivered and records the timestamp.
func (d *DB) MarkOutboxSent(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE outbox
		 SET status = 'sent', sent_at = CURRENT_TIMESTAMP, attempts = attempts + 1
		 WHERE id = ?`, id,
	)
	return err
}

// MarkOutboxFailed records a delivery failure without blocking future retries.
func (d *DB) MarkOutboxFailed(ctx context.Context, id int64, lastError string) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE outbox
		 SET status = 'failed', last_error = ?, attempts = attempts + 1
		 WHERE id = ?`, lastError, id,
	)
	return err
}

// AppendDeliveryLog writes one attempt record to delivery_log.
func (d *DB) AppendDeliveryLog(ctx context.Context, outboxID int64, success bool, errMsg string) error {
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO delivery_log (outbox_id, success, error) VALUES (?, ?, ?)`,
		outboxID, successInt, errMsg,
	)
	return err
}

// CountDeliveryLog returns number of delivery_log rows for an outbox item.
func (d *DB) CountDeliveryLog(ctx context.Context, outboxID int64) (int, error) {
	var n int
	err := d.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM delivery_log WHERE outbox_id = ?`, outboxID,
	).Scan(&n)
	return n, err
}

// GetFailedOutbox returns failed items with attempts < maxAttempts,
// enriched with the timestamp of the last delivery attempt.
// If no delivery_log entry exists, scheduled_at is used as fallback.
func (d *DB) GetFailedOutbox(ctx context.Context, maxAttempts int) ([]model.RetryItem, error) {
	rows, err := d.conn.QueryContext(ctx, `
		SELECT o.id, o.message_id, o.channel, o.attempts,
		       COALESCE(
		           (SELECT MAX(dl.attempted_at) FROM delivery_log dl WHERE dl.outbox_id = o.id),
		           o.scheduled_at
		       ) AS last_attempt_at
		FROM outbox o
		WHERE o.status = 'failed' AND o.attempts < ?
		ORDER BY o.id ASC
	`, maxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.RetryItem
	for rows.Next() {
		var it model.RetryItem
		var lastAttemptStr string
		if err := rows.Scan(&it.ID, &it.MessageID, &it.Channel, &it.Attempts, &lastAttemptStr); err != nil {
			return nil, err
		}
		it.LastAttemptAt = parseTime(lastAttemptStr)
		items = append(items, it)
	}
	return items, rows.Err()
}

// ResetToPending moves a failed item back to 'pending' so the Dispatcher retries it.
func (d *DB) ResetToPending(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE outbox SET status = 'pending', last_error = NULL WHERE id = ?`, id,
	)
	return err
}

// CancelStaleOutbox marks pending and failed outbox entries older than the given
// duration as 'cancelled'. Called on startup to discard backlog from previous runs.
func (d *DB) CancelStaleOutbox(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UTC().Format("2006-01-02 15:04:05")
	res, err := d.conn.ExecContext(ctx,
		`UPDATE outbox SET status = 'cancelled'
		 WHERE status IN ('pending', 'failed') AND scheduled_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountExhaustedOutbox returns the count of permanently failed items (attempts >= maxAttempts).
func (d *DB) CountExhaustedOutbox(ctx context.Context, maxAttempts int) (int, error) {
	var n int
	err := d.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE status = 'failed' AND attempts >= ?`, maxAttempts,
	).Scan(&n)
	return n, err
}

// parseTime handles SQLite datetime strings in multiple formats.
func parseTime(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
