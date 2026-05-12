package sender

import (
	"context"
	"fmt"
	"net/smtp"

	"abb/config"
	"abb/internal/model"
)

// SendFunc is the signature of smtp.SendMail, injected for testing.
type SendFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// EmailSender delivers messages via SMTP.
type EmailSender struct {
	cfg    config.EmailConfig
	sendFn SendFunc
}

// NewEmailSender returns an EmailSender. Pass nil sendFn to use smtp.SendMail.
func NewEmailSender(cfg config.EmailConfig, sendFn SendFunc) *EmailSender {
	if sendFn == nil {
		sendFn = smtp.SendMail
	}
	return &EmailSender{cfg: cfg, sendFn: sendFn}
}

func (e *EmailSender) Name() string { return "email" }

func (e *EmailSender) Send(_ context.Context, msg model.Message) error {
	addr := fmt.Sprintf("%s:%d", e.cfg.Host, e.cfg.Port)
	var auth smtp.Auth
	if e.cfg.Username != "" {
		auth = smtp.PlainAuth("", e.cfg.Username, e.cfg.Password, e.cfg.Host)
	}
	raw := BuildEmailMessage(e.cfg.From, e.cfg.To, msg)
	if err := e.sendFn(addr, auth, e.cfg.From, []string{e.cfg.To}, raw); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// BuildEmailMessage constructs a minimal RFC 2822 email body.
func BuildEmailMessage(from, to string, msg model.Message) []byte {
	subject := fmt.Sprintf("SMS from %s", msg.Address)
	body := msg.Body
	raw := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		from, to, subject, body,
	)
	return []byte(raw)
}
