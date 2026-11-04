// Package notify tells the user about failures. A channel is one way to reach
// the user; the application works without any channel and then says nothing.
//
// Channels are two small modules on net/http instead of a library: the
// requests are one POST each, nothing is added to the dependency tree, and the
// secrets (the bot token, the webhook address) stay out of every error text
// because this package scrubs them before anything leaves it.
package notify

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

// Names of the entries in the key store.
const (
	KeyTelegramToken = "telegram-bot-token"
	KeyWebhookURL    = "webhook-url"
)

// Channel names, as they appear in settings and in messages about delivery.
const (
	ChannelTelegram = "telegram"
	ChannelWebhook  = "webhook"
)

const (
	requestTimeout = 20 * time.Second
	// maxText keeps a message inside the limit of every channel (Telegram: 4096).
	maxText = 3900
)

// Message is what a channel delivers. Both parts are plain text.
type Message struct {
	Title string
	Body  string
}

// Text is the message as one string.
func (m Message) Text() string {
	text := m.Title
	if m.Body != "" {
		text += "\n\n" + m.Body
	}
	if runes := []rune(text); len(runes) > maxText {
		text = string(runes[:maxText]) + "..."
	}
	return text
}

// Channel delivers messages to the user.
type Channel interface {
	// Name is the channel's name in settings.
	Name() string
	Send(ctx context.Context, message Message) error
}

// Failure is one channel that could not deliver.
type Failure struct {
	Channel string
	Err     error
}

// Notifier sends one message through every channel it has.
type Notifier struct {
	Channels []Channel
}

// Empty reports whether there is nobody to tell.
func (n *Notifier) Empty() bool { return n == nil || len(n.Channels) == 0 }

// Send tries every channel. It returns the number of channels that took the
// message; the message counts as delivered when at least one did.
func (n *Notifier) Send(ctx context.Context, message Message) (delivered int, failures []Failure) {
	if n == nil {
		return 0, nil
	}
	for _, channel := range n.Channels {
		if err := channel.Send(ctx, message); err != nil {
			failures = append(failures, Failure{Channel: channel.Name(), Err: err})
			continue
		}
		delivered++
	}
	return delivered, failures
}

// Options tune how channels reach the network; tests set them.
type Options struct {
	Client *http.Client
	// TelegramBaseURL replaces the address of the Bot API.
	TelegramBaseURL string
}

// Build makes the channels the settings ask for, with their secrets from the key
// store. A channel that cannot be made is skipped and reported in the error; the
// others still work.
func Build(cfg config.Notify, store secrets.Store, opts Options) (*Notifier, error) {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	notifier := &Notifier{}
	var problems []*i18n.Error
	if cfg.Telegram != nil {
		token, err := store.Get(KeyTelegramToken)
		switch {
		case errors.Is(err, secrets.ErrNotFound):
			problems = append(problems, i18n.E("notify.telegram.token_missing").Hint("notify.telegram.token_missing.hint"))
		case err != nil:
			problems = append(problems, i18n.E("notify.secret_unavailable", ChannelTelegram).Because(err))
		default:
			notifier.Channels = append(notifier.Channels, &Telegram{Token: token, ChatID: cfg.Telegram.ChatID, BaseURL: opts.TelegramBaseURL, Client: client})
		}
	}
	if cfg.Webhook != nil && cfg.Webhook.Enabled {
		address, err := store.Get(KeyWebhookURL)
		switch {
		case errors.Is(err, secrets.ErrNotFound):
			problems = append(problems, i18n.E("notify.webhook.url_missing").Hint("notify.webhook.url_missing.hint"))
		case err != nil:
			problems = append(problems, i18n.E("notify.secret_unavailable", ChannelWebhook).Because(err))
		default:
			if err := CheckWebhookURL(address); err != nil {
				problems = append(problems, err)
				break
			}
			notifier.Channels = append(notifier.Channels, &Webhook{URL: address, Client: client})
		}
	}
	if len(problems) == 0 {
		return notifier, nil
	}
	return notifier, &i18n.Error{Key: "notify.channels_broken", HintKey: "notify.channels_broken.hint", Details: problems}
}

// CheckWebhookURL accepts an http or https address with a host. The address may
// hold a secret, so no message repeats it.
func CheckWebhookURL(address string) *i18n.Error {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return i18n.E("notify.webhook.bad_url").Hint("notify.webhook.bad_url.hint")
	}
	return nil
}

// scrub turns a transport error into one that is safe to show: the URL of a
// failed request holds the bot token or the webhook address, and net/http copies
// it into its error text.
func scrub(err error, secretValues ...string) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	text := err.Error()
	for _, secret := range secretValues {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "***")
		}
	}
	return errors.New(text)
}
