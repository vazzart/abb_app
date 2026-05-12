package sender

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/net/proxy"

	"abb/config"
	"abb/internal/model"
)

// reDigitRun matches three or more consecutive digits.
var reDigitRun = regexp.MustCompile(`\d{3,}`)

// WrapDigits surrounds every run of 3+ consecutive digits with backticks so
// OTP codes and numeric sequences stand out visually in Telegram messages.
// Applied only to the message body, not the sender address.
// Exported for testing.
func WrapDigits(text string) string {
	return reDigitRun.ReplaceAllString(text, "`$0`")
}

// TelegramSender delivers messages via the Telegram Bot API.
type TelegramSender struct {
	bot    *tgbotapi.BotAPI
	chatID int64
}

// NewTelegramSender validates the bot token and wires the optional SOCKS5 proxy.
func NewTelegramSender(token string, chatID int64, proxyCfg config.ProxyConfig) (*TelegramSender, error) {
	httpClient, err := BuildHTTPClient(proxyCfg)
	if err != nil {
		return nil, fmt.Errorf("build http client: %w", err)
	}

	bot, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		return nil, fmt.Errorf("telegram getMe: %w", err)
	}

	return &TelegramSender{bot: bot, chatID: chatID}, nil
}

func (t *TelegramSender) Name() string { return "telegram" }

func (t *TelegramSender) Send(_ context.Context, msg model.Message) error {
	transformed := msg
	transformed.Body = WrapDigits(msg.Body)
	m := tgbotapi.NewMessage(t.chatID, FormatMessage(transformed))
	_, err := t.bot.Send(m)
	return err
}

// BuildHTTPClient returns a plain http.DefaultClient or one routed through a SOCKS5 proxy.
// Exported so tests can verify client construction without a real bot token.
func BuildHTTPClient(cfg config.ProxyConfig) (*http.Client, error) {
	if !cfg.Enabled {
		return http.DefaultClient, nil
	}

	var auth *proxy.Auth
	if cfg.Username != "" {
		auth = &proxy.Auth{User: cfg.Username, Password: cfg.Password}
	}

	dialer, err := proxy.SOCKS5("tcp", cfg.Address, auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("socks5 dialer %s: %w", cfg.Address, err)
	}

	transport := &http.Transport{
		DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}
	return &http.Client{Transport: transport}, nil
}
