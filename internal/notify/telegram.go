package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// telegramAPI is the address of the Bot API.
const telegramAPI = "https://api.telegram.org"

// retryPause is how long to wait before the second attempt. Tests shorten it.
var retryPause = 2 * time.Second

// Telegram sends messages through a bot. It only calls sendMessage: it never
// reads updates, so a program that polls the same bot is not disturbed.
type Telegram struct {
	Token  string
	ChatID string
	// BaseURL replaces the Bot API address; tests point it at a local server.
	BaseURL string
	Client  *http.Client
}

// Name implements Channel.
func (*Telegram) Name() string { return ChannelTelegram }

// Send implements Channel.
func (t *Telegram) Send(ctx context.Context, message Message) error {
	payload, err := json.Marshal(map[string]any{
		"chat_id":                  t.ChatID,
		"text":                     message.Text(),
		"disable_web_page_preview": true,
	})
	if err != nil {
		return i18n.E("notify.telegram.failed").Because(err)
	}
	base := t.BaseURL
	if base == "" {
		base = telegramAPI
	}
	endpoint := strings.TrimRight(base, "/") + "/bot" + t.Token + "/sendMessage"

	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return last
			case <-time.After(retryPause):
			}
		}
		retry, err := t.post(ctx, endpoint, payload)
		if err == nil {
			return nil
		}
		last = err
		if !retry {
			break
		}
	}
	return last
}

// post makes one request and says whether another attempt could help.
func (t *Telegram) post(ctx context.Context, endpoint string, payload []byte) (retry bool, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false, i18n.E("notify.telegram.failed").Because(scrub(err, t.Token))
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := t.Client.Do(request)
	if err != nil {
		return true, i18n.E("notify.telegram.failed").Because(scrub(err, t.Token))
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode == http.StatusOK {
		return false, nil
	}
	var answer struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(body, &answer)
	description := strings.ReplaceAll(answer.Description, t.Token, "***")
	retry = response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	return retry, i18n.E("notify.telegram.rejected", response.StatusCode, description).Hint("notify.telegram.rejected.hint")
}
