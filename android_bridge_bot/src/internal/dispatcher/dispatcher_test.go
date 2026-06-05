package dispatcher_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"abb/internal/db"
	"abb/internal/dispatcher"
	"abb/internal/model"
	"abb/internal/sender"
)

func noopLogger() *zap.Logger {
	return zap.NewNop()
}

// --- MockSender ---------------------------------------------------------

type MockSender struct {
	mu      sync.Mutex
	name    string
	sendErr error
	calls   []model.Message
}

func newMock(name string, sendErr error) *MockSender {
	return &MockSender{name: name, sendErr: sendErr}
}

func (m *MockSender) Name() string { return m.name }

func (m *MockSender) Send(_ context.Context, msg model.Message) error {
	m.mu.Lock()
	m.calls = append(m.calls, msg)
	m.mu.Unlock()
	return m.sendErr
}

func (m *MockSender) Calls() []model.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.Message(nil), m.calls...)
}

// --- helpers ------------------------------------------------------------

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func seedMessage(t *testing.T, database *db.DB, androidID, address, body, channel string) (msgID int64, outboxID int64) {
	t.Helper()
	ctx := context.Background()
	msg := model.Message{
		AndroidID:  androidID,
		Address:    address,
		Body:       body,
		ReceivedAt: time.Now(),
	}
	id, _, err := database.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := database.CreateOutboxEntry(ctx, id, channel); err != nil {
		t.Fatalf("CreateOutboxEntry: %v", err)
	}
	items, err := database.GetPendingOutbox(ctx)
	if err != nil || len(items) == 0 {
		t.Fatalf("expected outbox entry after seed")
	}
	return id, items[len(items)-1].ID
}

func newDispatcher(database *db.DB, senders map[string]sender.Sender, rateDelay time.Duration) *dispatcher.Dispatcher {
	return dispatcher.New(database, senders, time.Hour, rateDelay, noopLogger())
}

// --- T3-01: успешная доставка -------------------------------------------

func TestDispatchOnce_Success(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	_, outboxID := seedMessage(t, database, "1", "+79001234567", "Привет", "telegram")

	mock := newMock("telegram", nil)
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": mock}, 0)
	disp.DispatchOnce(ctx)

	// outbox → sent
	items, _ := database.GetPendingOutbox(ctx)
	if len(items) != 0 {
		t.Errorf("expected 0 pending items after success, got %d", len(items))
	}

	// sender вызван ровно 1 раз
	if len(mock.Calls()) != 1 {
		t.Errorf("expected 1 sender call, got %d", len(mock.Calls()))
	}

	// delivery_log записан
	n, _ := database.CountDeliveryLog(ctx, outboxID)
	if n != 1 {
		t.Errorf("delivery_log: expected 1 entry, got %d", n)
	}
}

// --- T3-02: формат сообщения передаётся в Sender ------------------------

func TestDispatchOnce_MessageFormat(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	seedMessage(t, database, "1", "+79001234567", "Тест тело", "telegram")

	mock := newMock("telegram", nil)
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": mock}, 0)
	disp.DispatchOnce(ctx)

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call")
	}
	if calls[0].Address != "+79001234567" {
		t.Errorf("Address: got %q", calls[0].Address)
	}
	if calls[0].Body != "Тест тело" {
		t.Errorf("Body: got %q", calls[0].Body)
	}
}

// --- T3-03: ошибка sender → статус failed ------------------------------

func TestDispatchOnce_SenderError(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	_, outboxID := seedMessage(t, database, "1", "+7900", "body", "telegram")

	sendErr := errors.New("telegram: bad gateway")
	mock := newMock("telegram", sendErr)
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": mock}, 0)
	disp.DispatchOnce(ctx)

	// pending → 0 (failed, not pending)
	pending, _ := database.CountOutbox(ctx, "pending")
	if pending != 0 {
		t.Errorf("expected 0 pending, got %d", pending)
	}
	failed, _ := database.CountOutbox(ctx, "failed")
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}

	// delivery_log содержит запись с success=false
	n, _ := database.CountDeliveryLog(ctx, outboxID)
	if n != 1 {
		t.Errorf("delivery_log: expected 1 entry, got %d", n)
	}
}

// --- T3-04: delivery_log содержит все попытки --------------------------

func TestDispatchOnce_DeliveryLogAllAttempts(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	_, outboxID := seedMessage(t, database, "1", "+7900", "body", "telegram")

	// Первый вызов — ошибка
	failMock := newMock("telegram", errors.New("timeout"))
	dispatcher.New(database, map[string]sender.Sender{"telegram": failMock}, time.Hour, 0, noopLogger()).
		DispatchOnce(ctx)

	// Сбрасываем статус обратно в pending для повторной попытки
	database.UpdateOutboxStatus(ctx, outboxID, "pending", "") //nolint:errcheck

	// Второй вызов — успех
	okMock := newMock("telegram", nil)
	dispatcher.New(database, map[string]sender.Sender{"telegram": okMock}, time.Hour, 0, noopLogger()).
		DispatchOnce(ctx)

	n, _ := database.CountDeliveryLog(ctx, outboxID)
	if n != 2 {
		t.Errorf("delivery_log: expected 2 entries (1 fail + 1 success), got %d", n)
	}
}

// --- T3-05: успешно отправленные не отправляются повторно --------------

func TestDispatchOnce_NoRedeliveryOfSent(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	_, outboxID := seedMessage(t, database, "1", "+7900", "body", "telegram")
	database.MarkOutboxSent(ctx, outboxID) //nolint:errcheck

	mock := newMock("telegram", nil)
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": mock}, 0)
	disp.DispatchOnce(ctx)

	if len(mock.Calls()) != 0 {
		t.Errorf("expected no calls for already-sent item, got %d", len(mock.Calls()))
	}
}

// --- T3-06: rate limit — задержка между отправками ---------------------

func TestDispatchOnce_RateLimit(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	// 3 сообщения → 2 паузы
	for i := range 3 {
		seedMessage(t, database, string(rune('1'+i)), "+7900", "body", "telegram")
	}

	const delay = 30 * time.Millisecond
	mock := newMock("telegram", nil)
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": mock}, delay)

	start := time.Now()
	disp.DispatchOnce(ctx)
	elapsed := time.Since(start)

	minExpected := 2 * delay
	if elapsed < minExpected {
		t.Errorf("rate limit: elapsed %v < expected minimum %v", elapsed, minExpected)
	}
	if len(mock.Calls()) != 3 {
		t.Errorf("expected 3 calls, got %d", len(mock.Calls()))
	}
}

// --- T3-07: pending-записи предыдущего запуска доставляются при старте --

func TestDispatcher_DeliversPendingOnStartup(t *testing.T) {
	path := t.TempDir() + "/abb.db"
	ctx := context.Background()

	// «Предыдущий запуск»: сохранить SMS, не отправить
	d1, _ := db.Open(path)
	seedMessage(t, d1, "1", "+7900", "from prev run", "telegram")
	d1.Close()

	// «Новый запуск»
	d2, _ := db.Open(path)
	defer d2.Close()

	mock := newMock("telegram", nil)
	disp := dispatcher.New(d2, map[string]sender.Sender{"telegram": mock}, time.Hour, 0, noopLogger())
	disp.DispatchOnce(ctx)

	if len(mock.Calls()) != 1 {
		t.Errorf("expected 1 call for pending item from previous run, got %d", len(mock.Calls()))
	}
}

// --- незарегистрированный канал — помечается failed -------------------

func TestDispatchOnce_UnknownChannel(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	seedMessage(t, database, "1", "+7900", "body", "sms") // канал не зарегистрирован

	disp := newDispatcher(database, map[string]sender.Sender{}, 0)
	disp.DispatchOnce(ctx)

	failed, _ := database.CountOutbox(ctx, "failed")
	if failed != 1 {
		t.Errorf("expected 1 failed for unknown channel, got %d", failed)
	}
}

// ============================================================
// T6: мультиканальная доставка
// ============================================================

// seedMessageMultiChannel добавляет одно SMS в два канала и возвращает msgID.
func seedMessageMultiChannel(t *testing.T, database *db.DB, channels ...string) int64 {
	t.Helper()
	ctx := context.Background()
	msg := model.Message{
		AndroidID:  "mc1",
		Address:    "+7900",
		Body:       "multi",
		ReceivedAt: time.Now(),
	}
	id, _, err := database.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	for _, ch := range channels {
		if err := database.CreateOutboxEntry(ctx, id, ch); err != nil {
			t.Fatalf("CreateOutboxEntry(%s): %v", ch, err)
		}
	}
	return id
}

// T6-01: оба канала доставлены успешно
func TestDispatchOnce_TwoChannels_BothSucceed(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	seedMessageMultiChannel(t, database, "telegram", "email")

	tgMock := newMock("telegram", nil)
	emailMock := newMock("email", nil)
	disp := newDispatcher(database, map[string]sender.Sender{
		"telegram": tgMock,
		"email":    emailMock,
	}, 0)
	disp.DispatchOnce(ctx)

	pending, _ := database.CountOutbox(ctx, "pending")
	if pending != 0 {
		t.Errorf("expected 0 pending, got %d", pending)
	}
	sent, _ := database.CountOutbox(ctx, "sent")
	if sent != 2 {
		t.Errorf("expected 2 sent, got %d", sent)
	}
	if len(tgMock.Calls()) != 1 {
		t.Errorf("telegram: expected 1 call, got %d", len(tgMock.Calls()))
	}
	if len(emailMock.Calls()) != 1 {
		t.Errorf("email: expected 1 call, got %d", len(emailMock.Calls()))
	}
}

// T6-02: один канал падает, другой доставляется
func TestDispatchOnce_TwoChannels_OneFailsOtherSucceeds(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	seedMessageMultiChannel(t, database, "telegram", "email")

	tgMock := newMock("telegram", errors.New("timeout"))
	emailMock := newMock("email", nil)
	disp := newDispatcher(database, map[string]sender.Sender{
		"telegram": tgMock,
		"email":    emailMock,
	}, 0)
	disp.DispatchOnce(ctx)

	sent, _ := database.CountOutbox(ctx, "sent")
	if sent != 1 {
		t.Errorf("expected 1 sent, got %d", sent)
	}
	failed, _ := database.CountOutbox(ctx, "failed")
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}

// T6-03: канал в outbox не зарегистрирован в senders — помечается failed, другой работает
func TestDispatchOnce_TwoChannels_OneUnknown(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	seedMessageMultiChannel(t, database, "telegram", "email")

	tgMock := newMock("telegram", nil)
	// "email" не зарегистрирован
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": tgMock}, 0)
	disp.DispatchOnce(ctx)

	sent, _ := database.CountOutbox(ctx, "sent")
	if sent != 1 {
		t.Errorf("expected 1 sent (telegram), got %d", sent)
	}
	failed, _ := database.CountOutbox(ctx, "failed")
	if failed != 1 {
		t.Errorf("expected 1 failed (email unknown), got %d", failed)
	}
}

// ============================================================
// T7: DeliveryHooks — метрики вызываются после доставки
// ============================================================

// T7-05a: OnSent вызывается при успешной доставке
func TestDispatcher_Hooks_OnSent(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	seedMessage(t, database, "h1", "+7900", "body", "telegram")

	var sentCh string
	mock := newMock("telegram", nil)
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": mock}, 0)
	disp.SetHooks(&dispatcher.DeliveryHooks{
		OnSent:   func(ch string) { sentCh = ch },
		OnFailed: func(_ string) { t.Error("OnFailed must not be called on success") },
	})
	disp.DispatchOnce(ctx)

	if sentCh != "telegram" {
		t.Errorf("OnSent: got channel %q, want %q", sentCh, "telegram")
	}
}

// T7-05b: OnFailed вызывается при ошибке доставки
func TestDispatcher_Hooks_OnFailed(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	seedMessage(t, database, "h2", "+7900", "body", "telegram")

	var failedCh string
	mock := newMock("telegram", errors.New("timeout"))
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": mock}, 0)
	disp.SetHooks(&dispatcher.DeliveryHooks{
		OnSent:   func(_ string) { t.Error("OnSent must not be called on failure") },
		OnFailed: func(ch string) { failedCh = ch },
	})
	disp.DispatchOnce(ctx)

	if failedCh != "telegram" {
		t.Errorf("OnFailed: got channel %q, want %q", failedCh, "telegram")
	}
}

// T7-05c: nil hooks — нет паники
func TestDispatcher_Hooks_Nil(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	seedMessage(t, database, "h3", "+7900", "body", "telegram")

	mock := newMock("telegram", nil)
	disp := newDispatcher(database, map[string]sender.Sender{"telegram": mock}, 0)
	// No SetHooks call — hooks remain nil.
	disp.DispatchOnce(ctx) // must not panic
}

// T6-04: канал не входит в cfg.Channels → outbox-запись не создаётся
func TestSaveAndEnqueue_ChannelNotInConfig(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	// Добавляем SMS только для "telegram"; "email" не в списке каналов
	msg := model.Message{AndroidID: "x1", Address: "+7900", Body: "body", ReceivedAt: time.Now()}
	id, _, err := database.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	for _, ch := range []string{"telegram"} { // только один канал
		database.CreateOutboxEntry(ctx, id, ch) //nolint:errcheck
	}

	tgMock := newMock("telegram", nil)
	emailMock := newMock("email", nil)
	disp := newDispatcher(database, map[string]sender.Sender{
		"telegram": tgMock,
		"email":    emailMock,
	}, 0)
	disp.DispatchOnce(ctx)

	// email sender не должен получить вызов — outbox-запись для него не создавалась
	if len(emailMock.Calls()) != 0 {
		t.Errorf("email: expected 0 calls (not in channels), got %d", len(emailMock.Calls()))
	}
	if len(tgMock.Calls()) != 1 {
		t.Errorf("telegram: expected 1 call, got %d", len(tgMock.Calls()))
	}
}
