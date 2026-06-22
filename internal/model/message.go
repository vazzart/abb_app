package model

import "time"

type Message struct {
	AndroidID      string
	SubscriptionID string
	Address        string
	Body           string
	Translation    string
	DeviceName     string
	SimName        string
	ReceivedAt     time.Time
}

type OutboxItem struct {
	ID          int64
	MessageID   int64
	Channel     string
	Status      string
	Attempts    int
	LastError   string
	ScheduledAt time.Time
}

// RetryItem is a failed outbox entry enriched with the last attempt timestamp
// so the RetryWorker can calculate when the next retry is due.
type RetryItem struct {
	ID            int64
	MessageID     int64
	Channel       string
	Attempts      int
	LastAttemptAt time.Time
}
