package notify

import (
	"context"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Webhook posts messages to a URL.
type Webhook struct{}

// Send implements Channel.
func (Webhook) Send(context.Context, Message) error {
	return i18n.NotReady("notify.webhook", stage)
}
