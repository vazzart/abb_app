package sender

import (
	"context"
	"fmt"

	"abb/internal/model"
)

// Sender delivers a single message over one channel.
// Each new channel (Email, Slack, …) implements this interface and registers in Dispatcher.
type Sender interface {
	Name() string
	Send(ctx context.Context, msg model.Message) error
}

// FormatMessage produces the text sent to the recipient.
func FormatMessage(msg model.Message) string {
	return fmt.Sprintf("[%s] %s", msg.Address, msg.Body)
}
