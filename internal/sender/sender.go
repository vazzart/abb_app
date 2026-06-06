package sender

import (
	"context"
	"fmt"
	"strings"

	"abb/internal/model"
)

// Sender delivers a single message over one channel.
// Each new channel (Email, Slack, …) implements this interface and registers in Dispatcher.
type Sender interface {
	Name() string
	Send(ctx context.Context, msg model.Message) error
}

// FormatMessage formats a message as plain text for email and ntfy.
// Fields DeviceName and Translation are included only when non-empty.
func FormatMessage(msg model.Message) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "From: %s\n", msg.Address)
	fmt.Fprintf(&sb, "Time: %s\n", msg.ReceivedAt.Format("2006-01-02 15:04"))
	if msg.DeviceName != "" {
		fmt.Fprintf(&sb, "To: %s\n", msg.DeviceName)
	}
	fmt.Fprintf(&sb, "Text: %s", msg.Body)
	if msg.Translation != "" {
		fmt.Fprintf(&sb, "\nTranslate: %s", msg.Translation)
	}
	return sb.String()
}
