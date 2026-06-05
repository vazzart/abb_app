package adb_test

import (
	"testing"
	"time"

	"abb/internal/adb"
)

// T1-06: стандартный однострочный SMS
func TestParseSMSOutput_Standard(t *testing.T) {
	input := `Row: 0 _id=10488, address=HOME_CREDIT, body=Test message, date=1778609567319`

	msgs, err := adb.ParseSMSOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	m := msgs[0]
	assertEqual(t, "AndroidID", "10488", m.AndroidID)
	assertEqual(t, "Address", "HOME_CREDIT", m.Address)
	assertEqual(t, "Body", "Test message", m.Body)

	want := time.UnixMilli(1778609567319)
	if !m.ReceivedAt.Equal(want) {
		t.Errorf("ReceivedAt: got %v, want %v", m.ReceivedAt, want)
	}
}

// T1-07: многострочное тело
func TestParseSMSOutput_MultilineBody(t *testing.T) {
	input := "Row: 0 _id=10487, address=BANKFFIN, body=Код верификации 4670\neROdR70AswV, date=1778608280275"

	msgs, err := adb.ParseSMSOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	assertEqual(t, "Body", "Код верификации 4670\neROdR70AswV", msgs[0].Body)
}

// T1-08: Unicode и эмодзи
func TestParseSMSOutput_Unicode(t *testing.T) {
	input := `Row: 0 _id=1, address=+79001234567, body=Привет мир 🌍, date=1700000000000`

	msgs, err := adb.ParseSMSOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Body", "Привет мир 🌍", msgs[0].Body)
	assertEqual(t, "Address", "+79001234567", msgs[0].Address)
}

// T1-09: пустой inbox — пустой вывод
func TestParseSMSOutput_EmptyOutput(t *testing.T) {
	msgs, err := adb.ParseSMSOutput("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

// T1-09: пустой inbox — "No result found"
func TestParseSMSOutput_NoResult(t *testing.T) {
	msgs, err := adb.ParseSMSOutput("No result found.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

// T1-10: вывод ошибки прав — не паникует
func TestParseSMSOutput_PermissionDenied(t *testing.T) {
	input := `Exception occurred while executing 'query':
java.lang.SecurityException: Permission Denial: reading com.android.providers.telephony.SmsProvider`

	// Не паникует. Может вернуть ошибку или пустой срез — оба варианта допустимы.
	msgs, _ := adb.ParseSMSOutput(input)
	_ = msgs
}

// Несколько строк — правильный порядок и количество
func TestParseSMSOutput_MultipleRows(t *testing.T) {
	input := `Row: 0 _id=10488, address=HOME_CREDIT, body=First, date=1778609567319
Row: 1 _id=10487, address=BANKFFIN, body=Second, date=1778608280275
Row: 2 _id=10486, address=OSON, body=Third, date=1778607587034`

	msgs, err := adb.ParseSMSOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	assertEqual(t, "msgs[0].AndroidID", "10488", msgs[0].AndroidID)
	assertEqual(t, "msgs[1].AndroidID", "10487", msgs[1].AndroidID)
	assertEqual(t, "msgs[2].AndroidID", "10486", msgs[2].AndroidID)
}

// Тело содержит запятую — не ломает парсинг
func TestParseSMSOutput_CommaInBody(t *testing.T) {
	input := `Row: 0 _id=10488, address=HOME_CREDIT, body=Код 7108 в случае если это не Вы, обратитесь по номеру 7373!, date=1778609567319`

	msgs, err := adb.ParseSMSOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Код 7108 в случае если это не Вы, обратитесь по номеру 7373!"
	assertEqual(t, "Body", want, msgs[0].Body)
}

// Реальные данные с телефона — многострочный + запятая в теле
func TestParseSMSOutput_RealDeviceData(t *testing.T) {
	input := "Row: 0 _id=10488, address=HOME_CREDIT, body=ПОПЫТКА ВХОДА в мобильный банкинг с ДРУГОГО УСТРОЙСТВА.Код 7108 в случае если это не Вы, обратитесь по номеру 7373!, date=1778609567319\n" +
		"Row: 1 _id=10487, address=BANKFFIN, body=Код верификации 4670\neROdR70AswV, date=1778608280275"

	msgs, err := adb.ParseSMSOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].AndroidID != "10488" || msgs[1].AndroidID != "10487" {
		t.Errorf("wrong IDs: %s, %s", msgs[0].AndroidID, msgs[1].AndroidID)
	}
	if msgs[1].Body != "Код верификации 4670\neROdR70AswV" {
		t.Errorf("multiline body: got %q", msgs[1].Body)
	}
}

func assertEqual(t *testing.T, field, want, got string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", field, got, want)
	}
}
