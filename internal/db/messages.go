package db

import (
	"context"
	"time"

	"abb/internal/model"
)

// SaveMessage inserts the message if its android_id is new.
// Returns (rowID, true, nil) for new messages, (rowID, false, nil) for duplicates.
func (d *DB) SaveMessage(ctx context.Context, msg model.Message) (id int64, isNew bool, err error) {
	res, err := d.conn.ExecContext(ctx,
		`INSERT OR IGNORE INTO messages (android_id, address, body, device_name, sim_name, source, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		msg.AndroidID, msg.Address, msg.Body, msg.DeviceName, msg.SimName, msg.Source, msg.ReceivedAt,
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

// UpdateTranslation stores the translated text for a message.
func (d *DB) UpdateTranslation(ctx context.Context, id int64, translation string) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE messages SET translation = ? WHERE id = ?`,
		translation, id,
	)
	return err
}

// CountMessages returns total number of rows in messages.
func (d *DB) CountMessages(ctx context.Context) (int, error) {
	var n int
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&n)
	return n, err
}

// GetMessageByID returns a message by its DB id, including device/SIM name and translation.
func (d *DB) GetMessageByID(ctx context.Context, id int64) (model.Message, error) {
	var msg model.Message
	var received any
	err := d.conn.QueryRowContext(ctx,
		`SELECT android_id, address, body, COALESCE(device_name, ''), COALESCE(sim_name, ''),
		        COALESCE(source, ''), COALESCE(translation, ''), received_at
		 FROM messages WHERE id = ?`, id,
	).Scan(&msg.AndroidID, &msg.Address, &msg.Body, &msg.DeviceName, &msg.SimName,
		&msg.Source, &msg.Translation, &received)
	if err != nil {
		return msg, err
	}
	msg.ReceivedAt = scanTime(received)
	return msg, nil
}

// scanTime accepts received_at as the driver returns it: a time.Time when it
// could parse the stored text, otherwise the raw string. Rows written by v1.2.0
// hold "2006-01-02 15:04:05 +0300 SCTS", which the driver cannot parse.
func scanTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		return parseTime(t)
	case []byte:
		return parseTime(string(t))
	}
	return time.Time{}
}
