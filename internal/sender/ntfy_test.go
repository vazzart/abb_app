package sender_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"abb/config"
	"abb/internal/model"
	"abb/internal/sender"
)

func ntfyMsg() model.Message {
	return model.Message{Address: "+79001234567", Body: "Код: 4321"}
}

// T8-01: успешная доставка — сервер вернул 200
func TestNtfySender_Send_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.NtfyConfig{ServerURL: srv.URL, Topic: "sms"}
	s := sender.NewNtfySender(cfg, srv.Client())

	if err := s.Send(context.Background(), ntfyMsg()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// T8-02: сервер вернул 4xx — ошибка
func TestNtfySender_Send_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := config.NtfyConfig{ServerURL: srv.URL, Topic: "sms"}
	s := sender.NewNtfySender(cfg, srv.Client())

	if err := s.Send(context.Background(), ntfyMsg()); err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

// T8-03: заголовки Title, Priority, Content-Type выставляются корректно
func TestNtfySender_Send_Headers(t *testing.T) {
	var gotTitle, gotPriority, gotContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.NtfyConfig{ServerURL: srv.URL, Topic: "sms", Priority: "high"}
	s := sender.NewNtfySender(cfg, srv.Client())
	msg := model.Message{Address: "BANK", Body: "Ваш баланс 100₽"}

	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotTitle != "SMS from BANK" {
		t.Errorf("Title: got %q, want %q", gotTitle, "SMS from BANK")
	}
	if gotPriority != "high" {
		t.Errorf("Priority: got %q, want high", gotPriority)
	}
	if !strings.HasPrefix(gotContentType, "text/plain") {
		t.Errorf("Content-Type: got %q, want text/plain", gotContentType)
	}
}

// T8-04: Priority по умолчанию = "default"
func TestNtfySender_Send_DefaultPriority(t *testing.T) {
	var gotPriority string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPriority = r.Header.Get("Priority")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.NtfyConfig{ServerURL: srv.URL, Topic: "sms"} // Priority пустой
	s := sender.NewNtfySender(cfg, srv.Client())
	_ = s.Send(context.Background(), ntfyMsg())

	if gotPriority != "default" {
		t.Errorf("Priority: got %q, want default", gotPriority)
	}
}

// T8-05: токен передаётся как Bearer Authorization
func TestNtfySender_Send_BearerToken(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.NtfyConfig{ServerURL: srv.URL, Topic: "sms", Token: "secret123"}
	s := sender.NewNtfySender(cfg, srv.Client())
	_ = s.Send(context.Background(), ntfyMsg())

	if gotAuth != "Bearer secret123" {
		t.Errorf("Authorization: got %q, want Bearer secret123", gotAuth)
	}
}

// T8-06: без токена Authorization заголовок не выставляется
func TestNtfySender_Send_NoToken(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.NtfyConfig{ServerURL: srv.URL, Topic: "sms"}
	s := sender.NewNtfySender(cfg, srv.Client())
	_ = s.Send(context.Background(), ntfyMsg())

	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

// T8-07: trailing slash в ServerURL не дублируется
func TestNtfySender_Send_TrailingSlash(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.NtfyConfig{ServerURL: srv.URL + "/", Topic: "alerts"}
	s := sender.NewNtfySender(cfg, srv.Client())
	_ = s.Send(context.Background(), ntfyMsg())

	if gotPath != "/alerts" {
		t.Errorf("path: got %q, want /alerts", gotPath)
	}
}

// T8-08: Name() == "ntfy"
func TestNtfySender_Name(t *testing.T) {
	s := sender.NewNtfySender(config.NtfyConfig{}, nil)
	if s.Name() != "ntfy" {
		t.Errorf("Name: got %q, want ntfy", s.Name())
	}
}
