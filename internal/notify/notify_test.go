package notify

import (
	"context"
	"testing"
)

func TestChannelsAreDeclared(t *testing.T) {
	for _, channel := range []Channel{Telegram{}, Webhook{}} {
		if err := channel.Send(context.Background(), Message{Title: "t"}); err == nil {
			t.Errorf("%T should say it is not ready yet", channel)
		}
	}
}
