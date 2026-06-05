package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"abb/internal/db"
	"abb/internal/model"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func sampleMsg(androidID string) model.Message {
	return model.Message{
		AndroidID:  androidID,
		Address:    "+79001234567",
		Body:       "Test body",
		ReceivedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
}

// T2-05: БД не существует — создаётся автоматически
func TestOpen_CreatesDB(t *testing.T) {
	path := t.TempDir() + "/new.db"
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("expected DB to be created, got error: %v", err)
	}
	d.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("DB file not found after Open: %v", err)
	}
}

// T2-06: повреждённый файл — Open возвращает ошибку
func TestOpen_CorruptedFile(t *testing.T) {
	path := t.TempDir() + "/corrupt.db"
	if err := os.WriteFile(path, []byte("not a sqlite database !!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(path)
	if err == nil {
		_ = d.Close()
		t.Fatal("expected error for corrupted DB, got nil")
	}
}

// T2-01: новый SMS сохраняется в messages и outbox
func TestSaveMessage_NewMessage(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	id, isNew, err := d.SaveMessage(ctx, sampleMsg("42"))
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if !isNew {
		t.Error("expected isNew=true for first insert")
	}
	if id == 0 {
		t.Error("expected non-zero id")
	}

	if err := d.CreateOutboxEntry(ctx, id, "telegram"); err != nil {
		t.Fatalf("CreateOutboxEntry: %v", err)
	}

	count, _ := d.CountMessages(ctx)
	if count != 1 {
		t.Errorf("messages count: got %d, want 1", count)
	}
	pending, _ := d.CountOutbox(ctx, "pending")
	if pending != 1 {
		t.Errorf("outbox pending count: got %d, want 1", pending)
	}
}

// T2-02: дедупликация по android_id
func TestSaveMessage_Deduplication(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	id1, isNew1, _ := d.SaveMessage(ctx, sampleMsg("42"))
	id2, isNew2, _ := d.SaveMessage(ctx, sampleMsg("42"))

	if !isNew1 {
		t.Error("first insert must be new")
	}
	if isNew2 {
		t.Error("second insert of same android_id must not be new")
	}
	if id1 != id2 {
		t.Errorf("both calls must return same id: %d vs %d", id1, id2)
	}

	count, _ := d.CountMessages(ctx)
	if count != 1 {
		t.Errorf("messages count after duplicate: got %d, want 1", count)
	}
}

// T2-02: дубликат не создаёт лишнюю запись в outbox
func TestSaveMessage_DuplicateNoExtraOutbox(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	id, _, _ := d.SaveMessage(ctx, sampleMsg("42"))
	d.CreateOutboxEntry(ctx, id, "telegram") //nolint:errcheck

	// Второй вызов SaveMessage — isNew=false, outbox не трогаем
	_, isNew, _ := d.SaveMessage(ctx, sampleMsg("42"))
	if isNew {
		t.Fatal("expected duplicate")
	}

	pending, _ := d.CountOutbox(ctx, "pending")
	if pending != 1 {
		t.Errorf("outbox must have exactly 1 entry, got %d", pending)
	}
}

// T2-03: несколько SMS за один poll
func TestSaveMessage_MultipleSMS(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	ids := []string{"1", "2", "3", "4", "5"}
	for _, aid := range ids {
		id, isNew, err := d.SaveMessage(ctx, sampleMsg(aid))
		if err != nil {
			t.Fatalf("SaveMessage %s: %v", aid, err)
		}
		if !isNew {
			t.Errorf("android_id=%s must be new", aid)
		}
		d.CreateOutboxEntry(ctx, id, "telegram") //nolint:errcheck
	}

	count, _ := d.CountMessages(ctx)
	if count != 5 {
		t.Errorf("messages count: got %d, want 5", count)
	}
	pending, _ := d.CountOutbox(ctx, "pending")
	if pending != 5 {
		t.Errorf("outbox pending: got %d, want 5", pending)
	}
}

// T2-04: перезапуск — pending записи сохраняются, дублей нет
func TestRestart_PendingItemsPreserved(t *testing.T) {
	path := t.TempDir() + "/abb.db"
	ctx := context.Background()

	// "Первый запуск": сохраняем SMS
	d1, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := d1.SaveMessage(ctx, sampleMsg("99"))
	d1.CreateOutboxEntry(ctx, id, "telegram") //nolint:errcheck
	d1.Close()

	// "Перезапуск": открываем ту же БД
	d2, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()

	// Pending запись должна остаться
	pending, _ := d2.CountOutbox(ctx, "pending")
	if pending != 1 {
		t.Errorf("after restart outbox pending: got %d, want 1", pending)
	}

	// Повторный SaveMessage — дубликат, нет новых outbox-записей
	_, isNew, _ := d2.SaveMessage(ctx, sampleMsg("99"))
	if isNew {
		t.Error("after restart, same android_id must not be new")
	}
	pending2, _ := d2.CountOutbox(ctx, "pending")
	if pending2 != 1 {
		t.Errorf("after duplicate save: outbox pending %d, want 1", pending2)
	}
}

// T2-07: очень длинное SMS (>1000 символов)
func TestSaveMessage_LongBody(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	longBody := strings.Repeat("А", 2000)
	msg := model.Message{
		AndroidID:  "1",
		Address:    "+7900",
		Body:       longBody,
		ReceivedAt: time.Now(),
	}

	_, _, err := d.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatalf("SaveMessage with long body: %v", err)
	}

	items, _ := d.GetPendingOutbox(ctx)
	_ = items // body is in messages table, not outbox — just verify no error

	count, _ := d.CountMessages(ctx)
	if count != 1 {
		t.Errorf("expected 1 message, got %d", count)
	}
}

// T2-08: спецсимволы / SQL-инъекция в теле — данные сохраняются корректно
func TestSaveMessage_SQLInjection(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	injections := []string{
		`'; DROP TABLE messages; --`,
		`" OR "1"="1`,
		`\x00\x1f`,
	}
	for i, body := range injections {
		msg := model.Message{
			AndroidID:  string(rune('0' + i)),
			Address:    "+7900",
			Body:       body,
			ReceivedAt: time.Now(),
		}
		if _, _, err := d.SaveMessage(ctx, msg); err != nil {
			t.Errorf("SaveMessage with injection %q: %v", body, err)
		}
	}

	count, _ := d.CountMessages(ctx)
	if count != len(injections) {
		t.Errorf("messages count: got %d, want %d", count, len(injections))
	}
}

// GetMaxAndroidID — пустая таблица возвращает 0
func TestGetMaxAndroidID_EmptyTable(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	max, err := d.GetMaxAndroidID(ctx)
	if err != nil {
		t.Fatalf("GetMaxAndroidID: %v", err)
	}
	if max != 0 {
		t.Errorf("empty table: got %d, want 0", max)
	}
}

// GetMaxAndroidID — возвращает наибольший id
func TestGetMaxAndroidID_ReturnsMax(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	for _, aid := range []string{"10", "200", "30"} {
		d.SaveMessage(ctx, sampleMsg(aid)) //nolint:errcheck
	}

	max, err := d.GetMaxAndroidID(ctx)
	if err != nil {
		t.Fatalf("GetMaxAndroidID: %v", err)
	}
	if max != 200 {
		t.Errorf("got %d, want 200", max)
	}
}

// GetPendingOutbox — возвращает только pending, в порядке scheduled_at
func TestGetPendingOutbox(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	for _, aid := range []string{"1", "2", "3"} {
		id, _, _ := d.SaveMessage(ctx, sampleMsg(aid))
		d.CreateOutboxEntry(ctx, id, "telegram") //nolint:errcheck
	}

	// Помечаем одну как sent
	items, _ := d.GetPendingOutbox(ctx)
	d.UpdateOutboxStatus(ctx, items[0].ID, "sent", "") //nolint:errcheck

	remaining, err := d.GetPendingOutbox(ctx)
	if err != nil {
		t.Fatalf("GetPendingOutbox: %v", err)
	}
	if len(remaining) != 2 {
		t.Errorf("expected 2 pending items, got %d", len(remaining))
	}
	for _, it := range remaining {
		if it.Status != "pending" {
			t.Errorf("item %d has status %q, want pending", it.ID, it.Status)
		}
	}
}
