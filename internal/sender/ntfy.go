package sender

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"abb/config"
	"abb/internal/model"
)

// NtfySender delivers messages via the Ntfy HTTP API.
type NtfySender struct {
	cfg    config.NtfyConfig
	client *http.Client
}

// NewNtfySender returns a NtfySender. Pass nil client to use the default with a 10 s timeout.
func NewNtfySender(cfg config.NtfyConfig, client *http.Client) *NtfySender {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &NtfySender{cfg: cfg, client: client}
}

func (n *NtfySender) Name() string { return "ntfy" }

func (n *NtfySender) Send(ctx context.Context, msg model.Message) error {
	url := strings.TrimRight(n.cfg.ServerURL, "/") + "/" + n.cfg.Topic

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(FormatMessage(msg)))
	if err != nil {
		return fmt.Errorf("ntfy: create request: %w", err)
	}

	req.Header.Set("Title", "SMS from "+msg.Address)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	priority := n.cfg.Priority
	if priority == "" {
		priority = "default"
	}
	req.Header.Set("Priority", priority)

	if n.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.cfg.Token)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("ntfy: server returned %d", resp.StatusCode)
	}
	return nil
}
