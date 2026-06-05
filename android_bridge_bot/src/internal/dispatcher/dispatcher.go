package dispatcher

import (
	"context"
	"time"

	"go.uber.org/zap"

	"abb/internal/db"
	"abb/internal/model"
	"abb/internal/sender"
)

// DeliveryHooks contains optional callbacks invoked after each delivery attempt.
// All fields are nil-safe.
type DeliveryHooks struct {
	OnSent   func(channel string)
	OnFailed func(channel string)
}

// Dispatcher reads pending outbox items and delivers them via registered senders.
type Dispatcher struct {
	db        *db.DB
	senders   map[string]sender.Sender
	interval  time.Duration
	rateDelay time.Duration // minimum pause between consecutive sends
	log       *zap.Logger
	hooks     *DeliveryHooks
}

func New(database *db.DB, senders map[string]sender.Sender, interval, rateDelay time.Duration, log *zap.Logger) *Dispatcher {
	return &Dispatcher{
		db:        database,
		senders:   senders,
		interval:  interval,
		rateDelay: rateDelay,
		log:       log,
	}
}

// SetHooks wires optional delivery callbacks (e.g. Prometheus counter increments).
func (d *Dispatcher) SetHooks(h *DeliveryHooks) { d.hooks = h }

// Run dispatches pending items on startup and then on every interval tick.
func (d *Dispatcher) Run(ctx context.Context) {
	d.DispatchOnce(ctx)

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.DispatchOnce(ctx)
		}
	}
}

// DispatchOnce processes all currently pending outbox items.
// Exported so callers can trigger an immediate dispatch (e.g. right after saving a new SMS).
func (d *Dispatcher) DispatchOnce(ctx context.Context) {
	items, err := d.db.GetPendingOutbox(ctx)
	if err != nil {
		d.log.Error("get pending outbox", zap.Error(err))
		return
	}

	for i, item := range items {
		if ctx.Err() != nil {
			return
		}
		if i > 0 {
			select {
			case <-time.After(d.rateDelay):
			case <-ctx.Done():
				return
			}
		}
		d.deliver(ctx, item)
	}
}

func (d *Dispatcher) deliver(ctx context.Context, item model.OutboxItem) {
	sndr, ok := d.senders[item.Channel]
	if !ok {
		d.log.Warn("no sender registered for channel",
			zap.String("channel", item.Channel),
			zap.Int64("outbox_id", item.ID),
		)
		errMsg := "no sender for channel: " + item.Channel
		if err := d.db.MarkOutboxFailed(ctx, item.ID, errMsg); err != nil {
			d.log.Error("mark outbox failed", zap.Error(err), zap.Int64("outbox_id", item.ID))
		}
		if err := d.db.AppendDeliveryLog(ctx, item.ID, false, errMsg); err != nil {
			d.log.Error("append delivery log", zap.Error(err), zap.Int64("outbox_id", item.ID))
		}
		return
	}

	msg, err := d.db.GetMessageByID(ctx, item.MessageID)
	if err != nil {
		d.log.Error("get message for outbox item",
			zap.Error(err),
			zap.Int64("message_id", item.MessageID),
		)
		return
	}

	err = sndr.Send(ctx, msg)
	if err != nil {
		d.log.Error("delivery failed",
			zap.String("channel", item.Channel),
			zap.Int64("outbox_id", item.ID),
			zap.Error(err),
		)
		if dbErr := d.db.MarkOutboxFailed(ctx, item.ID, err.Error()); dbErr != nil {
			d.log.Error("mark outbox failed", zap.Error(dbErr), zap.Int64("outbox_id", item.ID))
		}
		if dbErr := d.db.AppendDeliveryLog(ctx, item.ID, false, err.Error()); dbErr != nil {
			d.log.Error("append delivery log", zap.Error(dbErr), zap.Int64("outbox_id", item.ID))
		}
		if d.hooks != nil && d.hooks.OnFailed != nil {
			d.hooks.OnFailed(item.Channel)
		}
		return
	}

	d.log.Info("delivered",
		zap.String("channel", item.Channel),
		zap.Int64("outbox_id", item.ID),
		zap.String("address", msg.Address),
	)
	if err := d.db.MarkOutboxSent(ctx, item.ID); err != nil {
		d.log.Error("mark outbox sent", zap.Error(err), zap.Int64("outbox_id", item.ID))
	}
	if err := d.db.AppendDeliveryLog(ctx, item.ID, true, ""); err != nil {
		d.log.Error("append delivery log", zap.Error(err), zap.Int64("outbox_id", item.ID))
	}
	if d.hooks != nil && d.hooks.OnSent != nil {
		d.hooks.OnSent(item.Channel)
	}
}
