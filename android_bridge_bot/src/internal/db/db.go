package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection with domain-level methods.
type DB struct {
	conn *sql.DB
}

// Open opens (or creates) the SQLite file at path and applies migrations.
// Use ":memory:" for tests.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

// GetMaxAndroidID returns the largest android_id stored so far (0 if table is empty).
// Used to initialise the Poller's lastID on restart so already-saved SMS are skipped.
func (d *DB) GetMaxAndroidID(ctx context.Context) (int64, error) {
	var v sql.NullInt64
	err := d.conn.QueryRowContext(ctx,
		`SELECT MAX(CAST(android_id AS INTEGER)) FROM messages`,
	).Scan(&v)
	if err != nil {
		return 0, err
	}
	return v.Int64, nil
}

func (d *DB) migrate() error {
	stmts := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS messages (
			id          INTEGER PRIMARY KEY,
			android_id  TEXT    UNIQUE NOT NULL,
			address     TEXT    NOT NULL,
			body        TEXT    NOT NULL,
			body_edited TEXT,
			received_at DATETIME NOT NULL,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			status      TEXT    NOT NULL DEFAULT 'pending'
		)`,
		`CREATE TABLE IF NOT EXISTS outbox (
			id           INTEGER PRIMARY KEY,
			message_id   INTEGER NOT NULL REFERENCES messages(id),
			channel      TEXT    NOT NULL,
			status       TEXT    NOT NULL DEFAULT 'pending',
			attempts     INTEGER NOT NULL DEFAULT 0,
			last_error   TEXT,
			scheduled_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			sent_at      DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS delivery_log (
			id           INTEGER PRIMARY KEY,
			outbox_id    INTEGER NOT NULL REFERENCES outbox(id),
			attempted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			success      INTEGER NOT NULL,
			error        TEXT
		)`,
	}
	for _, s := range stmts {
		if _, err := d.conn.Exec(s); err != nil {
			return fmt.Errorf("migrate stmt %q: %w", s[:min(len(s), 40)], err)
		}
	}
	return nil
}
