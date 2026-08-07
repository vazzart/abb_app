package translator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const yandexTranslateURL = "https://translate.api.cloud.yandex.net/translate/v2/translate"

type Yandex struct {
	url        string
	apiKey     string
	folderID   string
	targetLang string
	client     *http.Client
}

func NewYandex(apiKey, folderID, targetLang string) *Yandex {
	return NewYandexWithURL(yandexTranslateURL, apiKey, folderID, targetLang)
}

// NewYandexWithURL is like NewYandex but overrides the endpoint URL (for testing).
func NewYandexWithURL(url, apiKey, folderID, targetLang string) *Yandex {
	return &Yandex{
		url:        url,
		apiKey:     apiKey,
		folderID:   folderID,
		targetLang: targetLang,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

type translateRequest struct {
	FolderID           string   `json:"folderId"`
	Texts              []string `json:"texts"`
	TargetLanguageCode string   `json:"targetLanguageCode"`
	SourceLanguageCode string   `json:"sourceLanguageCode,omitempty"`
}

type translateResponse struct {
	Translations []struct {
		Text                 string `json:"text"`
		DetectedLanguageCode string `json:"detectedLanguageCode"`
	} `json:"translations"`
}

// Translate sends text to Yandex Translate and returns the translated text
// and the detected source language code (e.g. "ka", "en", "ru").
// Returns ("", targetLang, nil) when the text is already in the target language.
func (y *Yandex) Translate(ctx context.Context, text string) (translated, detectedLang string, err error) {
	reqBody := translateRequest{
		FolderID:           y.folderID,
		Texts:              []string{text},
		TargetLanguageCode: y.targetLang,
	}
	// Yandex's own language auto-detection reliably misidentifies
	// latin-transliterated Georgian as Uzbek/Turkish/etc (even with
	// hints), so force it explicitly when the heuristic recognizes it.
	forcedSource := ""
	if LooksLikeGeorgianLatin(text) {
		forcedSource = "ka"
		reqBody.SourceLanguageCode = forcedSource
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, y.url, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Api-Key "+y.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := y.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return "", "", fmt.Errorf("yandex translate: status %d: %s", resp.StatusCode, buf.String())
	}

	var result translateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode: %w", err)
	}
	if len(result.Translations) == 0 {
		return "", "", fmt.Errorf("yandex translate: empty response")
	}

	t := result.Translations[0]
	lang := t.DetectedLanguageCode
	if lang == "" && forcedSource != "" {
		// Yandex omits detectedLanguageCode when sourceLanguageCode was
		// given explicitly in the request.
		lang = forcedSource
	}
	return t.Text, lang, nil
}

// TargetLang returns the configured target language code.
func (y *Yandex) TargetLang() string { return y.targetLang }
