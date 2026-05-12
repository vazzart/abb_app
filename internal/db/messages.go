package db

import (
	"context"

	"abb/internal/model"
)

// SaveMessage inserts the message if its android_id is new.
// Returns (rowID, true, nil) for new messages, (rowID, false, nil) for duplicates.
func (d *DB) SaveMessage(ctx context.Context, msg model.Message) (id int64, isNew bool, err error) {
	res, err := d.conn.ExecContext(ctx,
		`INSERT OR IGNORE INTO messages (android_id, address, body, received_at)
		 VALUES (?, ?, ?, ?)`,
		msg.AndroidID, msg.Address, msg.Body, msg.ReceivedAt,
	)
	if err != nil {
		return 0, false, err
	}

	if n, _ := res.RowsAffected(); n > 0 {
		id, _ = res.LastInsertId()
		return id, true, nil
	}

	// Duplicate: return the existing row's id.
	err = d.conn.QueryRowContext(ctx,
		`SELECT id FROM messages WHERE android_id = ?`, msg.AndroidID,
	).Scan(&id)
	return id, false, err
}

// CountMessages returns total number of rows in messages.
func (d *DB) CountMessages(ctx context.Context) (int, error) {
	var n int
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&n)
	return n, err
}

// GetMessageByID returns a message by its DB id.
// Returns body_edited instead of body when an edit is present (T5 logic, no-op until Stage 5).
func (d *DB) GetMessageByID(ctx context.Context, id int64) (model.Message, error) {
	var msg model.Message
	err := d.conn.QueryRowContext(ctx,
		`SELECT android_id, address, COALESCE(body_edited, body), received_at
		 FROM messages WHERE id = ?`, id,
	).Scan(&msg.AndroidID, &msg.Address, &msg.Body, &msg.ReceivedAt)
	return msg, err
}
