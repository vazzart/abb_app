package sender_test

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"

	"abb/config"
	"abb/internal/model"
	"abb/internal/sender"
)

// T6-01: BuildEmailMessage формирует корректные заголовки и тело
func TestBuildEmailMessage_Format(t *testing.T) {
	msg := model.Message{Address: "+79001234567", Body: "Код 1234"}
	raw := sender.BuildEmailMessage("bot@example.com", "user@example.com", msg)
	s := string(raw)

	checks := []struct {
		field string
		want  string
	}{
		{"From header", "From: bot@example.com"},
		{"To header", "To: user@example.com"},
		{"Subject", "Subject: SMS from +79001234567"},
		{"body", "Код 1234"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.want) {
			t.Errorf("%s: want %q in output\ngot:\n%s", c.field, c.want, s)
		}
	}
}

// T6-02: BuildEmailMessage — multiline body сохраняется без изменений
func TestBuildEmailMessage_MultilineBody(t *testing.T) {
	body := "line1\nline2\nline3"
	msg := model.Message{Address: "BANK", Body: body}
	raw := sender.BuildEmailMessage("a@b.com", "c@d.com", msg)
	if !strings.Contains(string(raw), body) {
		t.Errorf("multiline body not preserved in email message")
	}
}

// T6-03: Send вызывает sendFn с правильными параметрами
func TestEmailSender_Send_CallsSmtp(t *testing.T) {
	var capturedAddr string
	var capturedFrom string
	var capturedTo []string
	var capturedMsg []byte

	fakeSend := func(addr string, _ smtp.Auth, from string, to []string, msg []byte) error {
		capturedAddr = addr
		capturedFrom = from
		capturedTo = to
		capturedMsg = msg
		return nil
	}

	cfg := config.EmailConfig{
		Enabled:  true,
		Host:     "smtp.example.com",
		Port:     587,
		Username: "user",
		Password: "pass",
		From:     "from@example.com",
		To:       "to@example.com",
	}
	s := sender.NewEmailSender(cfg, fakeSend)
	msg := model.Message{Address: "TEST", Body: "hello"}

	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedAddr != "smtp.example.com:587" {
		t.Errorf("addr: got %q, want %q", capturedAddr, "smtp.example.com:587")
	}
	if capturedFrom != "from@example.com" {
		t.Errorf("from: got %q", capturedFrom)
	}
	if len(capturedTo) != 1 || capturedTo[0] != "to@example.com" {
		t.Errorf("to: got %v", capturedTo)
	}
	if !strings.Contains(string(capturedMsg), "hello") {
		t.Errorf("msg body missing 'hello': %s", capturedMsg)
	}
}

// T6-04: Send propagates sendFn error
func TestEmailSender_Send_Error(t *testing.T) {
	sendErr := errors.New("connection refused")
	fakeSend := func(_ string, _ smtp.Auth, _ string, _ []string, _ []byte) error {
		return sendErr
	}

	cfg := config.EmailConfig{
		Host: "smtp.example.com",
		Port: 25,
		From: "a@b.com",
		To:   "c@d.com",
	}
	s := sender.NewEmailSender(cfg, fakeSend)
	err := s.Send(context.Background(), model.Message{Address: "X", Body: "y"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sendErr) {
		t.Errorf("expected wrapped sendErr, got: %v", err)
	}
}

// T6-05: EmailSender.Name() == "email"
func TestEmailSender_Name(t *testing.T) {
	s := sender.NewEmailSender(config.EmailConfig{}, func(_ string, _ smtp.Auth, _ string, _ []string, _ []byte) error { return nil })
	if s.Name() != "email" {
		t.Errorf("Name: got %q, want %q", s.Name(), "email")
	}
}

// T6-06: без username Auth не устанавливается (nil auth передаётся в sendFn)
func TestEmailSender_Send_NoAuth(t *testing.T) {
	var capturedAuth smtp.Auth
	fakeSend := func(_ string, a smtp.Auth, _ string, _ []string, _ []byte) error {
		capturedAuth = a
		return nil
	}
	cfg := config.EmailConfig{Host: "localhost", Port: 25, From: "a@b.com", To: "c@d.com"}
	s := sender.NewEmailSender(cfg, fakeSend)
	s.Send(context.Background(), model.Message{Address: "X", Body: "y"}) //nolint:errcheck
	if capturedAuth != nil {
		t.Errorf("expected nil auth when no username, got %v", capturedAuth)
	}
}
