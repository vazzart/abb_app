package translator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"abb/internal/translator"
)

func makeServer(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("Authorization header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestTranslate_Success(t *testing.T) {
	srv := makeServer(t, http.StatusOK, map[string]any{
		"translations": []map[string]any{
			{"text": "Привет, как дела?", "detectedLanguageCode": "ka"},
		},
	})
	defer srv.Close()

	tr := translator.NewYandexWithURL(srv.URL, "test-key", "folder1", "ru")

	translated, lang, err := tr.Translate(context.Background(), "gamarjoba, rogor xar?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if translated != "Привет, как дела?" {
		t.Errorf("translated = %q, want %q", translated, "Привет, как дела?")
	}
	if lang != "ka" {
		t.Errorf("detectedLang = %q, want %q", lang, "ka")
	}
}

func TestTranslate_AlreadyTargetLang(t *testing.T) {
	srv := makeServer(t, http.StatusOK, map[string]any{
		"translations": []map[string]any{
			{"text": "Всё хорошо", "detectedLanguageCode": "ru"},
		},
	})
	defer srv.Close()

	tr := translator.NewYandexWithURL(srv.URL, "test-key", "folder1", "ru")

	translated, lang, err := tr.Translate(context.Background(), "Всё хорошо")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "ru" {
		t.Errorf("detectedLang = %q, want %q", lang, "ru")
	}
	// caller should skip body_edited when lang == targetLang
	_ = translated
}

func TestTranslate_HTTPError(t *testing.T) {
	srv := makeServer(t, http.StatusUnauthorized, map[string]any{
		"message": "The token is invalid",
	})
	defer srv.Close()

	tr := translator.NewYandexWithURL(srv.URL, "bad-key", "folder1", "ru")

	_, _, err := tr.Translate(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}

func TestTranslate_EmptyResponse(t *testing.T) {
	srv := makeServer(t, http.StatusOK, map[string]any{
		"translations": []map[string]any{},
	})
	defer srv.Close()

	tr := translator.NewYandexWithURL(srv.URL, "test-key", "folder1", "ru")

	_, _, err := tr.Translate(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for empty translations, got nil")
	}
}

func TestTranslate_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// never respond
		<-r.Context().Done()
	}))
	defer srv.Close()

	tr := translator.NewYandexWithURL(srv.URL, "test-key", "folder1", "ru")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := tr.Translate(ctx, "hello")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestTargetLang(t *testing.T) {
	tr := translator.NewYandex("key", "folder", "ru")
	if tr.TargetLang() != "ru" {
		t.Errorf("TargetLang() = %q, want %q", tr.TargetLang(), "ru")
	}
}
