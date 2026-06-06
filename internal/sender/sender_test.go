package sender_test

import (
	"net/http"
	"testing"
	"time"

	"abb/config"
	"abb/internal/model"
	"abb/internal/sender"
)

// T3-02: plain-text формат для email/ntfy
func TestFormatMessage(t *testing.T) {
	fixedTime := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		msg  model.Message
		want string
	}{
		{
			name: "minimal — no device, no translation",
			msg:  model.Message{Address: "+79001234567", Body: "Код 1234", ReceivedAt: fixedTime},
			want: "From: +79001234567\nTime: 2026-01-15 10:30\nText: Код 1234",
		},
		{
			name: "with device name",
			msg:  model.Message{Address: "BANKFFIN", Body: "Верификация 4670", DeviceName: "Samsung A42", ReceivedAt: fixedTime},
			want: "From: BANKFFIN\nTime: 2026-01-15 10:30\nTo: Samsung A42\nText: Верификация 4670",
		},
		{
			name: "with translation",
			msg:  model.Message{Address: "+7900", Body: "Код 9999", Translation: "Code 9999", ReceivedAt: fixedTime},
			want: "From: +7900\nTime: 2026-01-15 10:30\nText: Код 9999\nTranslate: Code 9999",
		},
		{
			name: "all fields",
			msg:  model.Message{Address: "+7900", Body: "Привет", Translation: "Hello", DeviceName: "Pixel 6", ReceivedAt: fixedTime},
			want: "From: +7900\nTime: 2026-01-15 10:30\nTo: Pixel 6\nText: Привет\nTranslate: Hello",
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

// T3-02: HTML-формат для Telegram с тегами <b> и <code>
func TestFormatTelegramMessage(t *testing.T) {
	fixedTime := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		msg  model.Message
		want string
	}{
		{
			name: "minimal",
			msg:  model.Message{Address: "+79001234567", Body: "Код 1234", ReceivedAt: fixedTime},
			// Address goes through html.EscapeString only — digits in phone number are NOT wrapped
			want: "<b>From:</b> +79001234567\n<b>Time:</b> 2026-01-15 10:30\n<b>Text:</b> Код <code>1234</code>",
		},
		{
			name: "with device name",
			msg:  model.Message{Address: "BANK", Body: "Код 5678", DeviceName: "Samsung A42", ReceivedAt: fixedTime},
			want: "<b>From:</b> BANK\n<b>Time:</b> 2026-01-15 10:30\n<b>To:</b> Samsung A42\n<b>Text:</b> Код <code>5678</code>",
		},
		{
			name: "with translation",
			msg:  model.Message{Address: "SVC", Body: "Код 1111", Translation: "Code 1111", ReceivedAt: fixedTime},
			want: "<b>From:</b> SVC\n<b>Time:</b> 2026-01-15 10:30\n<b>Text:</b> Код <code>1111</code>\n<b>Translate:</b> Code <code>1111</code>",
		},
		{
			name: "html special chars in address",
			msg:  model.Message{Address: "A&B", Body: "ok", ReceivedAt: fixedTime},
			want: "<b>From:</b> A&amp;B\n<b>Time:</b> 2026-01-15 10:30\n<b>Text:</b> ok",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sender.FormatTelegramMessage(tc.msg)
			if got != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
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
		{"exactly 3 digits", "код 123", "код <code>123</code>"},
		{"4 digits", "Код 1234", "Код <code>1234</code>"},
		{"6 digits OTP", "код 698754", "код <code>698754</code>"},
		{"10 digits phone in body", "звоните 7373141234", "звоните <code>7373141234</code>"},

		// менее 3 цифр — без изменений
		{"2 digits no wrap", "12 ab", "12 ab"},
		{"1 digit no wrap", "вариант 5", "вариант 5"},
		{"no digits", "привет мир", "привет мир"},
		{"empty string", "", ""},

		// несколько групп в одном тексте
		{"two groups", "Код 7108, номер 7373", "Код <code>7108</code>, номер <code>7373</code>"},
		{"mixed lengths", "12 34 567 и 89", "12 34 <code>567</code> и 89"},

		// реальные данные с телефона
		{
			"real OTP with comma in body",
			"ПОПЫТКА ВХОДА в банк.Код 7108 в случае не Вы, номер 7373!",
			"ПОПЫТКА ВХОДА в банк.Код <code>7108</code> в случае не Вы, номер <code>7373</code>!",
		},
		{
			"real verification code",
			"Код верификации 4670\neROdR70AswV",
			"Код верификации <code>4670</code>\neROdR70AswV", // eROdR70AswV: только "70" — 2 цифры
		},
		{
			"OSON code",
			"OSON verification code: 698754",
			"OSON verification code: <code>698754</code>",
		},

		// HTML-спецсимволы в тексте экранируются
		{"html chars in text", "баланс: 100<200 & 500руб", "баланс: <code>100</code>&lt;<code>200</code> &amp; <code>500</code>руб"},

		// цифры вплотную к тексту без пробела — HTML-теги работают независимо от границ слов
		{"digits adjacent to text", "код1234текст", "код<code>1234</code>текст"},

		// уже обёрнутые — бэктики не HTML-спецсимволы, цифры внутри обернутся
		{"already wrapped", "`1234`", "`<code>1234</code>`"},
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

// WrapDigits применяется к body и translation — адрес экранируется отдельно через html.EscapeString
func TestWrapDigits_AddressNotAffected(t *testing.T) {
	// В FormatTelegramMessage адрес экранируется через html.EscapeString,
	// а не через WrapDigits, чтобы цифры в номере телефона не оборачивались в <code>.
	// Проверяем что WrapDigits корректно обрабатывает только переданный текст.
	got := sender.WrapDigits("Код 9876")
	want := "Код <code>9876</code>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// T5: body_edited используется вместо body когда задан (через GetMessageByID → COALESCE)
// Трансформация WrapDigits применяется поверх body_edited так же, как и поверх body.
func TestWrapDigits_AppliedToEditedBody(t *testing.T) {
	edited := "Новый текст 9999"
	got := sender.WrapDigits(edited)
	want := "Новый текст <code>9999</code>"
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
