package sender_test

import (
	"net/http"
	"testing"
	"time"

	"abb/config"
	"abb/internal/model"
	"abb/internal/sender"
)

// T3-02: формат сообщения — [address] body
func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  model.Message
		want string
	}{
		{
			name: "phone number",
			msg:  model.Message{Address: "+79001234567", Body: "Код 1234"},
			want: "[+79001234567] Код 1234",
		},
		{
			name: "short code",
			msg:  model.Message{Address: "BANKFFIN", Body: "Верификация 4670\neROdR70AswV"},
			want: "[BANKFFIN] Верификация 4670\neROdR70AswV",
		},
		{
			name: "empty body",
			msg:  model.Message{Address: "+7900", Body: ""},
			want: "[+7900] ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sender.FormatMessage(tc.msg)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// T3-02: ReceivedAt не влияет на форматирование
func TestFormatMessage_ReceivedAtIgnored(t *testing.T) {
	msg := model.Message{
		Address:    "TEST",
		Body:       "hello",
		ReceivedAt: time.Now(),
	}
	got := sender.FormatMessage(msg)
	want := "[TEST] hello"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// T3-09: прокси выключен — возвращается http.DefaultClient
func TestBuildHTTPClient_ProxyDisabled(t *testing.T) {
	cfg := config.ProxyConfig{Enabled: false}
	client, err := sender.BuildHTTPClient(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client != http.DefaultClient {
		t.Error("expected http.DefaultClient when proxy is disabled")
	}
}

// T3-08: прокси включён — возвращается клиент с кастомным Transport
func TestBuildHTTPClient_ProxyEnabled(t *testing.T) {
	cfg := config.ProxyConfig{
		Enabled:  true,
		Address:  "127.0.0.1:1080",
		Username: "",
		Password: "",
	}
	client, err := sender.BuildHTTPClient(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == http.DefaultClient {
		t.Error("expected custom client when proxy is enabled")
	}
	if client.Transport == nil {
		t.Error("expected non-nil Transport for proxy client")
	}
}

// T3-11: прокси с аутентификацией — клиент создаётся без ошибки
func TestBuildHTTPClient_ProxyWithAuth(t *testing.T) {
	cfg := config.ProxyConfig{
		Enabled:  true,
		Address:  "213.171.27.132:443",
		Username: "userproxy",
		Password: "GfhjkmGHJRCBvazz",
	}
	client, err := sender.BuildHTTPClient(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.Transport == nil {
		t.Error("expected non-nil Transport")
	}
}

// ============================================================
// T5: WrapDigits — обёртка цифровых последовательностей
// ============================================================

func TestWrapDigits(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		// базовые случаи
		{"exactly 3 digits", "код 123", "код `123`"},
		{"4 digits", "Код 1234", "Код `1234`"},
		{"6 digits OTP", "код 698754", "код `698754`"},
		{"10 digits phone in body", "звоните 7373141234", "звоните `7373141234`"},

		// менее 3 цифр — без изменений
		{"2 digits no wrap", "12 ab", "12 ab"},
		{"1 digit no wrap", "вариант 5", "вариант 5"},
		{"no digits", "привет мир", "привет мир"},
		{"empty string", "", ""},

		// несколько групп в одном тексте
		{"two groups", "Код 7108, номер 7373", "Код `7108`, номер `7373`"},
		{"mixed lengths", "12 34 567 и 89", "12 34 `567` и 89"},

		// реальные данные с телефона
		{
			"real OTP with comma in body",
			"ПОПЫТКА ВХОДА в банк.Код 7108 в случае не Вы, номер 7373!",
			"ПОПЫТКА ВХОДА в банк.Код `7108` в случае не Вы, номер `7373`!",
		},
		{
			"real verification code",
			"Код верификации 4670\neROdR70AswV",
			"Код верификации `4670`\neROdR70AswV", // eROdR70AswV: только "70" — 2 цифры
		},
		{
			"OSON code",
			"OSON verification code: 698754",
			"OSON verification code: `698754`",
		},

		// уже обёрнутые — применяем повторно (idempotency не гарантируется, документируем)
		{"already wrapped", "`1234`", "``1234``"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sender.WrapDigits(tc.input)
			if got != tc.want {
				t.Errorf("\ninput: %q\n  got: %q\n want: %q", tc.input, got, tc.want)
			}
		})
	}
}

// WrapDigits не затрагивает адрес — применяется только к body
func TestWrapDigits_AddressNotAffected(t *testing.T) {
	// Адрес +79001234567 содержит 11 цифр, но FormatMessage оборачивает его в [...]
	// WrapDigits вызывается только для body, поэтому адрес остаётся нетронутым.
	msg := model.Message{
		Address: "+79001234567",
		Body:    "Код 9876",
	}
	// Симулируем что делает TelegramSender.Send: WrapDigits только на body
	body := sender.WrapDigits(msg.Body)
	formatted := sender.FormatMessage(model.Message{Address: msg.Address, Body: body})
	want := "[+79001234567] Код `9876`"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

// T5: body_edited используется вместо body когда задан (через GetMessageByID → COALESCE)
// Трансформация WrapDigits применяется поверх body_edited так же, как и поверх body.
func TestWrapDigits_AppliedToEditedBody(t *testing.T) {
	edited := "Новый текст 9999"
	got := sender.WrapDigits(edited)
	want := "Новый текст `9999`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// T3-14: невалидный адрес прокси — proxy.SOCKS5 не возвращает ошибку при создании диалера,
// ошибка возникает только при Dial(). Документируем это поведение.
func TestBuildHTTPClient_InvalidAddress_NoPanicAtCreation(t *testing.T) {
	cfg := config.ProxyConfig{
		Enabled: true,
		Address: "not-a-valid-host:99999",
	}
	// proxy.SOCKS5 не проверяет достижимость адреса при создании —
	// ошибка возникнет только при первом Send().
	client, err := sender.BuildHTTPClient(cfg)
	if err != nil {
		// Если ошибка всё же возвращается — тоже допустимо (fail-fast).
		t.Logf("BuildHTTPClient returned error (fail-fast): %v", err)
		return
	}
	if client == nil {
		t.Error("expected non-nil client")
	}
}
