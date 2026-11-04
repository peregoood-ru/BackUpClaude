package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

const testToken = "123456:SECRET-token-value"

func init() { retryPause = time.Millisecond }

// describe renders an error the way the user sees it.
func describe(err error) string {
	d := i18n.New("en").Describe(err)
	text := d.Message + " " + d.Hint
	for _, detail := range d.Details {
		text += " " + detail
	}
	return text
}

func TestTelegramSendsOneMessageThroughSendMessageOnly(t *testing.T) {
	var paths []string
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	channel := &Telegram{Token: testToken, ChatID: "42", BaseURL: server.URL, Client: server.Client()}
	if err := channel.Send(context.Background(), Message{Title: "Title", Body: "Body"}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/bot"+testToken+"/sendMessage" {
		t.Errorf("requests = %v: the channel must only call sendMessage", paths)
	}
	if got["chat_id"] != "42" || got["text"] != "Title\n\nBody" {
		t.Errorf("payload = %v", got)
	}
}

func TestTelegramErrorsNeverHoldTheToken(t *testing.T) {
	// A server that is gone: net/http puts the whole URL, token included, into its error.
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	client := server.Client()
	server.Close()
	channel := &Telegram{Token: testToken, ChatID: "42", BaseURL: url, Client: client}
	err := channel.Send(context.Background(), Message{Title: "x"})
	if err == nil {
		t.Fatal("expected a network error")
	}
	if text := describe(err); strings.Contains(text, "SECRET") || strings.Contains(text, testToken) {
		t.Errorf("the token leaked: %s", text)
	}
}

func TestTelegramRejectionIsExplainedAndNotRetriedWhenFinal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized ` + testToken + `"}`))
	}))
	defer server.Close()
	channel := &Telegram{Token: testToken, ChatID: "42", BaseURL: server.URL, Client: server.Client()}
	err := channel.Send(context.Background(), Message{Title: "x"})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("err = %v, calls = %d: a 401 will not get better", err, calls.Load())
	}
	text := describe(err)
	if !strings.Contains(text, "401") || strings.Contains(text, "SECRET") {
		t.Errorf("text = %s", text)
	}
}

func TestTelegramRetriesAServerError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	channel := &Telegram{Token: testToken, ChatID: "42", BaseURL: server.URL, Client: server.Client()}
	if err := channel.Send(context.Background(), Message{Title: "x"}); err != nil || calls.Load() != 2 {
		t.Fatalf("err = %v, calls = %d", err, calls.Load())
	}
}

func TestWebhookPostsJSON(t *testing.T) {
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s", r.Method, r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
	}))
	defer server.Close()
	fixed := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	channel := &Webhook{URL: server.URL + "/hook/SECRET", Client: server.Client(), Now: func() time.Time { return fixed }}
	if err := channel.Send(context.Background(), Message{Title: "T", Body: "B"}); err != nil {
		t.Fatal(err)
	}
	if got["title"] != "T" || got["body"] != "B" || got["text"] != "T\n\nB" || got["time"] != "2026-11-03T12:00:00Z" {
		t.Errorf("payload = %v", got)
	}
}

func TestWebhookErrorsNeverHoldTheAddress(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	address := server.URL + "/hook/SECRET"
	client := server.Client()
	server.Close()
	err := (&Webhook{URL: address, Client: client}).Send(context.Background(), Message{Title: "x"})
	if err == nil {
		t.Fatal("expected a network error")
	}
	if text := describe(err); strings.Contains(text, "SECRET") || strings.Contains(text, "/hook") {
		t.Errorf("the address leaked: %s", text)
	}

	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer rejecting.Close()
	err = (&Webhook{URL: rejecting.URL + "/hook/SECRET", Client: rejecting.Client()}).Send(context.Background(), Message{Title: "x"})
	if text := describe(err); !strings.Contains(text, "403") || strings.Contains(text, "SECRET") {
		t.Errorf("text = %s", text)
	}
}

func TestMessageTextIsCut(t *testing.T) {
	long := Message{Title: "t", Body: strings.Repeat("я", 10000)}
	if runes := []rune(long.Text()); len(runes) > maxText+3 {
		t.Errorf("%d characters", len(runes))
	}
	if (Message{Title: "only"}).Text() != "only" {
		t.Error("a message without body is just the title")
	}
}

func TestBuildNeedsNothingWithoutChannels(t *testing.T) {
	notifier, err := Build(config.Notify{}, secrets.NewMemory(), Options{})
	if err != nil || !notifier.Empty() {
		t.Fatalf("%v %+v", err, notifier)
	}
	if delivered, failures := notifier.Send(context.Background(), Message{Title: "x"}); delivered != 0 || len(failures) != 0 {
		t.Errorf("%d %v", delivered, failures)
	}
	var none *Notifier
	if !none.Empty() {
		t.Error("a nil notifier is empty")
	}
}

func TestBuildTakesSecretsFromTheKeyStore(t *testing.T) {
	store := secrets.NewMemory()
	cfg := config.Notify{Telegram: &config.Telegram{ChatID: "42"}, Webhook: &config.Webhook{Enabled: true}}

	notifier, err := Build(cfg, store, Options{})
	if err == nil || !notifier.Empty() {
		t.Fatalf("channels without secrets must be reported: %v", err)
	}
	text := describe(err)
	if !strings.Contains(text, "token") {
		t.Errorf("text = %s", text)
	}

	_ = store.Set(KeyTelegramToken, testToken)
	_ = store.Set(KeyWebhookURL, "https://example.org/hook/SECRET")
	notifier, err = Build(cfg, store, Options{})
	if err != nil || len(notifier.Channels) != 2 {
		t.Fatalf("%v %+v", err, notifier)
	}

	_ = store.Set(KeyWebhookURL, "not an address")
	notifier, err = Build(cfg, store, Options{})
	if err == nil || len(notifier.Channels) != 1 || notifier.Channels[0].Name() != ChannelTelegram {
		t.Fatalf("a bad address must not stop the other channel: %v %+v", err, notifier)
	}
	if text := describe(err); strings.Contains(text, "not an address") {
		t.Errorf("the address was echoed: %s", text)
	}
}

func TestDisabledWebhookIsIgnored(t *testing.T) {
	notifier, err := Build(config.Notify{Webhook: &config.Webhook{Enabled: false}}, secrets.NewMemory(), Options{})
	if err != nil || !notifier.Empty() {
		t.Fatalf("%v %+v", err, notifier)
	}
}

type stubChannel struct {
	name string
	err  error
	sent []Message
}

func (s *stubChannel) Name() string { return s.name }
func (s *stubChannel) Send(_ context.Context, m Message) error {
	s.sent = append(s.sent, m)
	return s.err
}

func TestOneBrokenChannelDoesNotStopTheOther(t *testing.T) {
	broken := &stubChannel{name: "a", err: i18n.E("notify.webhook.failed")}
	working := &stubChannel{name: "b"}
	notifier := &Notifier{Channels: []Channel{broken, working}}
	delivered, failures := notifier.Send(context.Background(), Message{Title: "x"})
	if delivered != 1 || len(failures) != 1 || failures[0].Channel != "a" || len(working.sent) != 1 {
		t.Errorf("%d %+v", delivered, failures)
	}
}
