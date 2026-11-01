package notify

import (
	"context"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Telegram sends messages through a bot.
type Telegram struct{}

// Send implements Channel.
func (Telegram) Send(context.Context, Message) error {
	return i18n.NotReady("notify.telegram", stage)
}
