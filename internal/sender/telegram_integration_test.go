//go:build integration

package sender_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"abb/config"
	"abb/internal/adb"
	"abb/internal/model"
	"abb/internal/sender"
	"abb/internal/translator"
)

// loadEnvFile reads KEY=VALUE pairs from a file into the environment.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		os.Setenv(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}
	return scanner.Err()
}

// mustTelegramSender loads credentials from testdata/telegram.env and creates a sender.
// Skips the test if credentials are missing.
func mustTelegramSender(t *testing.T) *sender.TelegramSender {
	t.Helper()
	_ = loadEnvFile("testdata/telegram.env")

	token := os.Getenv("TELEGRAM_TOKEN")
	chatIDStr := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatIDStr == "" {
		t.Skip("TELEGRAM_TOKEN and TELEGRAM_CHAT_ID required; copy testdata/telegram.env.example → testdata/telegram.env")
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		t.Fatalf("invalid TELEGRAM_CHAT_ID: %v", err)
	}

	s, err := sender.NewTelegramSender(token, chatID, config.ProxyConfig{})
	if err != nil {
		t.Fatalf("NewTelegramSender: %v", err)
	}
	return s
}

// maybeTranslate calls Yandex Translate if credentials are present in env.
// Returns empty string if credentials are missing or if text is already in target lang.
func maybeTranslate(ctx context.Context, t *testing.T, text string) string {
	t.Helper()
	apiKey := os.Getenv("YANDEX_API_KEY")
	folderID := os.Getenv("YANDEX_FOLDER_ID")
	if apiKey == "" || folderID == "" {
		return ""
	}
	tr := translator.NewYandex(apiKey, folderID, "ru")
	translated, lang, err := tr.Translate(ctx, text)
	if err != nil {
		t.Logf("translate: %v (skipping)", err)
		return ""
	}
	if lang == tr.TargetLang() {
		t.Logf("translate: text already in %s, skipping", lang)
		return ""
	}
	t.Logf("translate: %s → %s", lang, tr.TargetLang())
	return translated
}

// TestFullMessageFlow simulates the complete pipeline:
//
//  1. ADB raw output → ParseSMSOutput → model.Message  (like the Poller does)
//  2. DeviceName set on message  (like the Poller does after getprop ro.product.model)
//  3. Optional real Yandex translation  (like saveAndEnqueue does)
//  4. TelegramSender.Send → FormatTelegramMessage → Telegram API
//
// Run:
//
//	go test -tags integration ./internal/sender/ -v -run TestFullMessageFlow
//
// Credentials in testdata/telegram.env (see telegram.env.example).
func TestFullMessageFlow(t *testing.T) {
	s := mustTelegramSender(t)
	ctx := context.Background()

	// ── 1. Парсинг из сырого ADB-вывода (как это делает Poller) ──────────────
	rawADB := `Row: 0 _id=1001, address=+79001234567, body=Ваш код подтверждения: 123456. Не сообщайте никому., date=1749212640000`

	parsed, err := adb.ParseSMSOutput(rawADB)
	if err != nil || len(parsed) == 0 {
		t.Fatalf("ParseSMSOutput: %v", err)
	}
	smsFromADB := parsed[0]
	smsFromADB.DeviceName = "Samsung Galaxy A42" // как Poller ставит после getprop

	t.Run("1. OTP из ADB-вывода (парсинг + имя устройства)", func(t *testing.T) {
		printCase(t, smsFromADB)
		if err := s.Send(ctx, smsFromADB); err != nil {
			t.Errorf("Send: %v", err)
		}
	})

	// ── 2. SMS с несколькими группами цифр ───────────────────────────────────
	t.Run("2. Несколько цифровых групп", func(t *testing.T) {
		msg := model.Message{
			Address:    "KAPITALBANK",
			Body:       "ПОПЫТКА ВХОДА в банк. Код 7108 в случае не Вы, звоните 7373141234!",
			DeviceName: "Xiaomi Redmi Note 12",
			ReceivedAt: time.Now(),
		}
		printCase(t, msg)
		if err := s.Send(ctx, msg); err != nil {
			t.Errorf("Send: %v", err)
		}
	})

	// ── 3. SMS с переводом (реальный Yandex если есть ключи, иначе mock) ─────
	t.Run("3. Перевод (OSON verification code)", func(t *testing.T) {
		body := "OSON verification code: 698754. Do not share with anyone."
		msg := model.Message{
			Address:    "OSON",
			Body:       body,
			DeviceName: "Samsung Galaxy A42",
			ReceivedAt: time.Now(),
		}
		// Пытаемся перевести через реальный API; если ключей нет — ставим заготовку
		if tr := maybeTranslate(ctx, t, body); tr != "" {
			msg.Translation = tr
			t.Logf("real translation: %s", tr)
		} else {
			msg.Translation = "Код подтверждения OSON: 698754. Не сообщайте никому."
			t.Log("using mock translation (no YANDEX_API_KEY/YANDEX_FOLDER_ID)")
		}
		printCase(t, msg)
		if err := s.Send(ctx, msg); err != nil {
			t.Errorf("Send: %v", err)
		}
	})

	// ── 4. SMS с HTML-символами — проверка экранирования ─────────────────────
	t.Run("4. HTML-спецсимволы в тексте", func(t *testing.T) {
		msg := model.Message{
			Address:    "+77001234567",
			Body:       "Баланс: 100<200 тенге & остаток 500 руб. Код: 998877",
			DeviceName: "Pixel 6",
			ReceivedAt: time.Now(),
		}
		printCase(t, msg)
		if err := s.Send(ctx, msg); err != nil {
			t.Errorf("Send: %v", err)
		}
	})

	// ── 5. SMS без имени устройства — поле To не показывается ────────────────
	t.Run("5. Без имени устройства", func(t *testing.T) {
		msg := model.Message{
			Address:    "BANKFFIN",
			Body:       "Верификация 4670\neROdR70AswV",
			ReceivedAt: time.Now(),
		}
		printCase(t, msg)
		if err := s.Send(ctx, msg); err != nil {
			t.Errorf("Send: %v", err)
		}
	})

	// ── 6. Короткие числа — без обёртки (< 3 цифр) ───────────────────────────
	t.Run("6. Короткие числа не оборачиваются", func(t *testing.T) {
		msg := model.Message{
			Address:    "SERVICE",
			Body:       "Вариант 5 из 12. Попытка 2 из 3.",
			DeviceName: "Samsung Galaxy A42",
			ReceivedAt: time.Now(),
		}
		printCase(t, msg)
		if err := s.Send(ctx, msg); err != nil {
			t.Errorf("Send: %v", err)
		}
	})

	// ── 7. Стартовое уведомление (как app посылает при запуске) ──────────────
	t.Run("7. Стартовое уведомление приложения", func(t *testing.T) {
		msg := model.Message{
			Address:    "ABB",
			Body:       "Started. Version: 1.0.4\nDevices connected: 1\n  Samsung Galaxy A42 (R5CR31XXXX)",
			ReceivedAt: time.Now(),
		}
		printCase(t, msg)
		if err := s.Send(ctx, msg); err != nil {
			t.Errorf("Send: %v", err)
		}
	})
}

func printCase(t *testing.T, msg model.Message) {
	t.Helper()
	fmt.Printf("\n── %s ──\n", t.Name())
	fmt.Printf("  From:      %s\n", msg.Address)
	fmt.Printf("  Time:      %s\n", msg.ReceivedAt.Format("2006-01-02 15:04"))
	if msg.DeviceName != "" {
		fmt.Printf("  To:        %s\n", msg.DeviceName)
	}
	fmt.Printf("  Text:      %s\n", msg.Body)
	if msg.Translation != "" {
		fmt.Printf("  Translate: %s\n", msg.Translation)
	}
	fmt.Printf("  → HTML:    %s\n", sender.FormatTelegramMessage(msg))
}
