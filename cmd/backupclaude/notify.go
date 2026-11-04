package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

// botToken is the shape of a Telegram bot token. A wrong one is refused before
// it reaches the key store, without repeating what was typed.
var botToken = regexp.MustCompile(`^[0-9]{3,15}:[A-Za-z0-9_-]{20,60}$`)

const defaultTokenField = "token"

func (a *app) newNotifyCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "notify",
		Short: a.tr.T("cli.notify.short"),
		Long:  a.tr.T("cli.notify.long"),
		Args:  a.noArgs,
		RunE:  a.showGroup,
	}
	group.AddCommand(a.newNotifyTelegramCommand(), a.newNotifyWebhookCommand(), a.newNotifyTestCommand(), a.newNotifyRemoveCommand())
	return group
}

// loadConfig reads the settings; the commands that change them go through it.
func (a *app) loadConfig() (config.Layout, *config.Config, error) {
	layout, err := config.Resolve()
	if err != nil {
		return layout, nil, err
	}
	cfg, err := config.Load(layout.ConfigFile())
	return layout, cfg, err
}

// notifierFor makes the channels of the settings. The error lists the channels
// that could not be made; the notifier still holds the ones that work.
func (a *app) notifierFor(cfg *config.Config) (*notify.Notifier, error) {
	return notify.Build(cfg.Notify, a.deps.secrets(), a.deps.notify)
}

// secretLine reads one line of a secret from the input, when the person asked
// for it with a flag or sits at a terminal. The line goes only to the key
// store: it is never printed or logged.
func (a *app) secretLine(fromStdin bool, prompt string) (string, bool, error) {
	if !fromStdin && !a.deps.terminal() {
		return "", false, nil
	}
	if a.deps.terminal() {
		fmt.Fprint(a.stderr, prompt, " ")
	}
	line, err := a.input().ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, i18n.E("notify.read_failed").Because(err)
	}
	line = strings.TrimSpace(line)
	return line, line != "", nil
}

// channelAnswer is the answer of the commands that set a channel up. It holds no
// secret and no personal id.
type channelAnswer struct {
	Channel string `json:"channel"`
	Saved   bool   `json:"saved"`
	// SecretUpdated is true when a new token or address went to the key store.
	SecretUpdated bool `json:"secret_updated"`
}

func (a *app) newNotifyTelegramCommand() *cobra.Command {
	var chat, tokenFile, tokenField string
	var tokenStdin bool
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: a.tr.T("cli.notify.telegram.short"),
		Long:  a.tr.T("cli.notify.telegram.long"),
		Args:  a.noArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.setTelegram(chat, tokenFile, tokenField, tokenStdin)
		},
	}
	cmd.Flags().StringVar(&chat, "chat-id", "", a.tr.T("flag.notify.chat_id"))
	cmd.Flags().StringVar(&tokenFile, "token-file", "", a.tr.T("flag.notify.token_file"))
	cmd.Flags().StringVar(&tokenField, "token-field", defaultTokenField, a.tr.T("flag.notify.token_field"))
	cmd.Flags().BoolVar(&tokenStdin, "token-stdin", false, a.tr.T("flag.notify.token_stdin"))
	return cmd
}

func (a *app) setTelegram(chat, tokenFile, tokenField string, tokenStdin bool) error {
	layout, cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if chat == "" && cfg.Notify.Telegram != nil {
		chat = cfg.Notify.Telegram.ChatID
	}
	if chat == "" {
		return i18n.E("notify.telegram.chat_required").Hint("notify.telegram.chat_required.hint").Exit(exitUsage)
	}
	store := a.deps.secrets()

	var token string
	switch {
	case tokenFile != "":
		if token, err = readTokenFile(tokenFile, tokenField); err != nil {
			return err
		}
	default:
		line, ok, err := a.secretLine(tokenStdin, a.tr.T("notify.telegram.token_prompt"))
		if err != nil {
			return err
		}
		if ok {
			token = line
		}
	}
	if token == "" {
		if _, err := store.Get(notify.KeyTelegramToken); err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				return i18n.E("notify.telegram.token_required").Hint("notify.telegram.token_required.hint").Exit(exitUsage)
			}
			return err
		}
	} else if !botToken.MatchString(token) {
		return i18n.E("notify.telegram.token_bad").Hint("notify.telegram.token_bad.hint")
	}

	cfg.Notify.Telegram = &config.Telegram{ChatID: chat}
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		return err
	}
	if token != "" {
		if err := store.Set(notify.KeyTelegramToken, token); err != nil {
			return err
		}
	}
	answer := channelAnswer{Channel: notify.ChannelTelegram, Saved: true, SecretUpdated: token != ""}
	return a.emit(answer, func(w io.Writer) {
		fmt.Fprintln(w, a.tr.T("notify.telegram.saved"))
		if answer.SecretUpdated {
			fmt.Fprintln(w, a.tr.T("notify.token_stored"))
		}
		fmt.Fprintln(w, a.tr.T("notify.try_test"))
	})
}

// readTokenFile takes the token out of a JSON file such as the settings of
// another program that already uses the bot. The file is only read.
func readTokenFile(path, field string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", i18n.E("notify.token_file.unreadable", path).Hint("notify.token_file.unreadable.hint").Because(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return "", i18n.E("notify.token_file.not_json", path).Hint("notify.token_file.unreadable.hint")
	}
	var token string
	if raw, ok := fields[field]; !ok || json.Unmarshal(raw, &token) != nil || strings.TrimSpace(token) == "" {
		return "", i18n.E("notify.token_file.no_field", path, field).Hint("notify.token_file.no_field.hint")
	}
	return strings.TrimSpace(token), nil
}

func (a *app) newNotifyWebhookCommand() *cobra.Command {
	var urlStdin bool
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: a.tr.T("cli.notify.webhook.short"),
		Long:  a.tr.T("cli.notify.webhook.long"),
		Args:  a.noArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.setWebhook(urlStdin)
		},
	}
	cmd.Flags().BoolVar(&urlStdin, "url-stdin", false, a.tr.T("flag.notify.url_stdin"))
	return cmd
}

func (a *app) setWebhook(urlStdin bool) error {
	layout, cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	address, ok, err := a.secretLine(urlStdin, a.tr.T("notify.webhook.prompt"))
	if err != nil {
		return err
	}
	if !ok {
		return i18n.E("notify.webhook.url_required").Hint("notify.webhook.url_required.hint").Exit(exitUsage)
	}
	if bad := notify.CheckWebhookURL(address); bad != nil {
		return bad
	}
	cfg.Notify.Webhook = &config.Webhook{Enabled: true}
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		return err
	}
	if err := a.deps.secrets().Set(notify.KeyWebhookURL, address); err != nil {
		return err
	}
	answer := channelAnswer{Channel: notify.ChannelWebhook, Saved: true, SecretUpdated: true}
	return a.emit(answer, func(w io.Writer) {
		fmt.Fprintln(w, a.tr.T("notify.webhook.saved"))
		fmt.Fprintln(w, a.tr.T("notify.try_test"))
	})
}

func (a *app) newNotifyRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:       "remove telegram|webhook",
		Short:     a.tr.T("cli.notify.remove.short"),
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{notify.ChannelTelegram, notify.ChannelWebhook},
		RunE: func(_ *cobra.Command, args []string) error {
			return a.removeChannel(args[0])
		},
	}
}

func (a *app) removeChannel(name string) error {
	layout, cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	var key string
	switch name {
	case notify.ChannelTelegram:
		cfg.Notify.Telegram, key = nil, notify.KeyTelegramToken
	case notify.ChannelWebhook:
		cfg.Notify.Webhook, key = nil, notify.KeyWebhookURL
	default:
		return i18n.E("notify.unknown_channel", name).Hint("notify.unknown_channel.hint").Exit(exitUsage)
	}
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		return err
	}
	if err := a.deps.secrets().Delete(key); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	return a.emit(channelAnswer{Channel: name}, func(w io.Writer) {
		fmt.Fprintln(w, a.tr.T("notify.removed", name))
	})
}

func (a *app) newNotifyTestCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: a.tr.T("cli.notify.test.short"),
		Long:  a.tr.T("cli.notify.test.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.notifyTest(cmd.Context())
		},
	}
}

// deliveryFailure is a channel that did not take a message.
type deliveryFailure struct {
	Channel string `json:"channel"`
	Error   string `json:"error"`
}

type testAnswer struct {
	Delivered int               `json:"delivered"`
	Failures  []deliveryFailure `json:"failures,omitempty"`
}

func (a *app) notifyTest(ctx context.Context) error {
	_, cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	notifier, buildErr := a.notifierFor(cfg)
	if notifier.Empty() {
		if buildErr != nil {
			return buildErr
		}
		return i18n.E("notify.none_configured").Hint("notify.none_configured.hint")
	}
	delivered, failures := notifier.Send(ctx, a.testMessage())
	answer := testAnswer{Delivered: delivered, Failures: a.describeFailures(failures)}
	if err := a.emit(answer, func(w io.Writer) {
		fmt.Fprintln(w, a.tr.T("notify.test.sent", delivered, len(notifier.Channels)))
		for _, failure := range answer.Failures {
			fmt.Fprintln(w, "  "+a.tr.T("notify.test.failure", failure.Channel, failure.Error))
		}
	}); err != nil {
		return err
	}
	if buildErr != nil {
		return buildErr
	}
	if len(failures) > 0 {
		return errReported
	}
	return nil
}

func (a *app) testMessage() notify.Message {
	return notify.Message{
		Title: a.tr.T("notify.test.title"),
		Body:  a.tr.T("notify.test.body", hostName(), time.Now().Format("02.01.2006 15:04")),
	}
}

func (a *app) describeFailures(failures []notify.Failure) []deliveryFailure {
	var described []deliveryFailure
	for _, failure := range failures {
		description := a.tr.Describe(failure.Err)
		text := description.Message
		if description.Hint != "" {
			text += " " + description.Hint
		}
		described = append(described, deliveryFailure{Channel: failure.Channel, Error: text})
	}
	return described
}

// hostName names this computer in messages, so that a person with several
// computers knows which one is talking.
func hostName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "?"
	}
	return name
}
