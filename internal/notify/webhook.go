package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Webhook posts messages to a URL as JSON: {"title", "body", "text", "time"}.
// "text" is the title and the body together, which is what chat services that
// accept incoming webhooks usually want.
type Webhook struct {
	// URL may hold a secret; it comes from the key store and is never printed.
	URL    string
	Client *http.Client
	// Now gives the time put into the payload; tests fix it.
	Now func() time.Time
}

// Name implements Channel.
func (*Webhook) Name() string { return ChannelWebhook }

// Send implements Channel.
func (w *Webhook) Send(ctx context.Context, message Message) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	payload, err := json.Marshal(map[string]string{
		"title": message.Title,
		"body":  message.Body,
		"text":  message.Text(),
		"time":  now().Format(time.RFC3339),
	})
	if err != nil {
		return i18n.E("notify.webhook.failed").Because(err)
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return last
			case <-time.After(retryPause):
			}
		}
		retry, err := w.post(ctx, payload)
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

func (w *Webhook) post(ctx context.Context, payload []byte) (retry bool, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(payload))
	if err != nil {
		return false, i18n.E("notify.webhook.failed").Because(scrub(err, w.URL))
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := w.Client.Do(request)
	if err != nil {
		return true, i18n.E("notify.webhook.failed").Because(scrub(err, w.URL))
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return false, nil
	}
	retry = response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	return retry, i18n.E("notify.webhook.rejected", response.StatusCode).Hint("notify.webhook.rejected.hint")
}
