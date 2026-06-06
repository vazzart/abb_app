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
	"abb/internal/model"
	"abb/internal/sender"
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

func TestTelegramSend_HTMLFormatting(t *testing.T) {
	_ = loadEnvFile("testdata/telegram.env")

	token := os.Getenv("TELEGRAM_TOKEN")
	chatIDStr := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatIDStr == "" {
		t.Skip("TELEGRAM_TOKEN and TELEGRAM_CHAT_ID required; copy testdata/telegram.env.example to testdata/telegram.env and fill in values")
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		t.Fatalf("invalid TELEGRAM_CHAT_ID: %v", err)
	}

	s, err := sender.NewTelegramSender(token, chatID, config.ProxyConfig{})
	if err != nil {
		t.Fatalf("NewTelegramSender: %v", err)
	}

	cases := []struct {
		name string
		body string
	}{
		{"OTP рядом с текстом", "Ваш код подтверждения: 123456. Не сообщайте никому."},
		{"Несколько групп", "ПОПЫТКА ВХОДА. Код 7108 в случае не Вы, звоните 7373141234!"},
		{"Цифры вплотную к тексту", "Верификация4670eROdR70AswV"},
		{"HTML-символы в тексте", "Баланс: 100<200 & скидка 30%! Код: 998877"},
		{"Без цифр", "Обычное сообщение без кодов"},
		{"Короткие числа без обёртки", "Вариант 5 из 12 доступен"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := model.Message{
				Address:    "+79001234567",
				Body:       tc.body,
				ReceivedAt: time.Now(),
			}
			if err := s.Send(context.Background(), msg); err != nil {
				t.Errorf("Send failed: %v", err)
			}
			fmt.Printf("  sent: %q\n  html: %q\n\n", tc.body, sender.WrapDigits(tc.body))
		})
	}
}
