package sender

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"regexp"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/net/proxy"

	"abb/config"
	"abb/internal/model"
)

// reDigitRun matches three or more consecutive digits.
var reDigitRun = regexp.MustCompile(`\d{3,}`)

// WrapDigits HTML-escapes the text and wraps every run of 3+ consecutive
// digits in <code> tags so they render as monospace in Telegram HTML mode.
// HTML escaping happens first so any < > & in the SMS body are safe.
// Exported for testing.
func WrapDigits(text string) string {
	escaped := html.EscapeString(text)
	return reDigitRun.ReplaceAllString(escaped, "<code>$0</code>")
}

// FormatTelegramMessage formats a message as Telegram HTML.
// Labels are bold; digits in body and translation are wrapped in <code>.
// Exported for testing.
func FormatTelegramMessage(msg model.Message) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>From:</b> %s\n", html.EscapeString(msg.Address))
	fmt.Fprintf(&sb, "<b>Time:</b> %s\n", msg.ReceivedAt.Format("2006-01-02 15:04"))
	if msg.DeviceName != "" {
		fmt.Fprintf(&sb, "<b>To:</b> %s\n", html.EscapeString(msg.DeviceName))
	}
	fmt.Fprintf(&sb, "<b>Text:</b> %s", WrapDigits(msg.Body))
	if msg.Translation != "" {
		fmt.Fprintf(&sb, "\n<b>Translate:</b> %s", WrapDigits(msg.Translation))
	}
	return sb.String()
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
	m := tgbotapi.NewMessage(t.chatID, FormatTelegramMessage(msg))
	m.ParseMode = tgbotapi.ModeHTML
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
