package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/profile"
	"github.com/peregoood-ru/BackUpClaude/internal/watch"
)

const (
	// Built from pieces so that secret scanners do not take a made-up token for a real one.
	testBotToken = "123456789" + ":" + "AAH-test_token_value_" + "0123456789abcdef"
	testChatID   = "424242"
)

// chatServer plays the Bot API: it keeps what was sent and can be told to refuse.
type chatServer struct {
	*httptest.Server
	mu     sync.Mutex
	sent   []sentMessage
	refuse bool
}

type sentMessage struct {
	Path string
	Chat string
	Text string
}

func newChatServer(t *testing.T) *chatServer {
	t.Helper()
	c := &chatServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.refuse {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.Unmarshal(body, &payload)
		c.sent = append(c.sent, sentMessage{Path: r.URL.Path, Chat: payload.ChatID, Text: payload.Text})
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *chatServer) messages() []sentMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sentMessage(nil), c.sent...)
}

// withTelegram sets the world up as Alexey's: a bot token in the key store, a
// chat id in the settings, a local server in place of Telegram.
func (w *backupWorld) withTelegram(t *testing.T) *chatServer {
	t.Helper()
	server := newChatServer(t)
	w.notify = notify.Options{Client: server.Client(), TelegramBaseURL: server.URL}
	if err := w.store.Set(notify.KeyTelegramToken, testBotToken); err != nil {
		t.Fatal(err)
	}
	changeConfig(t, func(cfg *config.Config) { cfg.Notify.Telegram = &config.Telegram{ChatID: testChatID} })
	return server
}

func changeConfig(t *testing.T, change func(*config.Config)) {
	t.Helper()
	layout, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	change(cfg)
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
}

// noSecrets fails the test when output holds the token or the chat id.
func noSecrets(t *testing.T, what string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(text, "AAH-test") || strings.Contains(text, testBotToken) || strings.Contains(text, testChatID) {
			t.Errorf("%s leaked a secret: %s", what, text)
		}
	}
}

func TestNotifyTelegramKeepsTheTokenInTheKeyStoreAndTheChatInTheSettings(t *testing.T) {
	w := newBackupWorld(t, "")
	w.stdin = testBotToken + "\n"
	r := w.run("notify", "telegram", "--chat-id", testChatID, "--token-stdin", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	noSecrets(t, "notify telegram", r.stdout, r.stderr)
	if got, err := w.store.Get(notify.KeyTelegramToken); err != nil || got != testBotToken {
		t.Fatalf("the token is not in the key store: %q %v", got, err)
	}
	layout, _ := config.Resolve()
	data, err := os.ReadFile(layout.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), testChatID) || strings.Contains(string(data), "AAH-test") {
		t.Errorf("settings: %s", data)
	}
}

func TestNotifyTelegramImportsTheTokenFromAFileWithoutPrintingIt(t *testing.T) {
	w := newBackupWorld(t, "")
	file := filepath.Join(t.TempDir(), "other program", "config.json")
	fakehome.Write(t, file, fmt.Sprintf(`{"token": %q, "ownerId": 1, "other": "x"}`, testBotToken))
	r := w.run("notify", "telegram", "--chat-id", testChatID, "--token-file", file)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	noSecrets(t, "import", r.stdout, r.stderr)
	if got, _ := w.store.Get(notify.KeyTelegramToken); got != testBotToken {
		t.Error("the token was not imported")
	}
	// The source file is only read.
	if after, _ := os.ReadFile(file); !strings.Contains(string(after), testBotToken) {
		t.Error("the source file was changed")
	}

	other := newBackupWorld(t, "")
	r = other.run("notify", "telegram", "--chat-id", testChatID, "--token-file", file, "--token-field", "missing")
	if r.code == 0 || !strings.Contains(r.stderr, "missing") {
		t.Errorf("a missing field must be explained: %+v", r)
	}
	noSecrets(t, "bad field", r.stderr)
}

func TestNotifyTelegramRefusesAWrongTokenWithoutEchoingIt(t *testing.T) {
	w := newBackupWorld(t, "")
	w.stdin = "hunter2-not-a-token\n"
	r := w.run("notify", "telegram", "--chat-id", testChatID, "--token-stdin")
	if r.code == 0 || strings.Contains(r.stderr, "hunter2") {
		t.Fatalf("%+v", r)
	}
	if _, err := w.store.Get(notify.KeyTelegramToken); err == nil {
		t.Error("a wrong token must not be stored")
	}
	w.stdin = testBotToken + "\n"
	if r := w.run("notify", "telegram", "--chat-id", "not a chat", "--token-stdin"); r.code == 0 {
		t.Errorf("a wrong chat id must be refused: %+v", r)
	}
}

func TestNotifyTelegramNeedsAChatAndAToken(t *testing.T) {
	w := newBackupWorld(t, "")
	if r := w.run("notify", "telegram"); r.code != exitUsage {
		t.Errorf("no chat id: %+v", r)
	}
	if r := w.run("notify", "telegram", "--chat-id", testChatID); r.code != exitUsage {
		t.Errorf("no token anywhere: %+v", r)
	}
	// With a token already stored, the chat can be changed alone.
	_ = w.store.Set(notify.KeyTelegramToken, testBotToken)
	if r := w.run("notify", "telegram", "--chat-id", "-100123"); r.code != 0 {
		t.Errorf("%+v", r)
	}
}

func TestNotifyTestSendsOnlySendMessage(t *testing.T) {
	w := newBackupWorld(t, "")
	server := w.withTelegram(t)
	r := w.run("notify", "test", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	noSecrets(t, "notify test", r.stdout, r.stderr)
	sent := server.messages()
	if len(sent) != 1 || sent[0].Path != "/bot"+testBotToken+"/sendMessage" || sent[0].Chat != testChatID {
		t.Fatalf("sent = %+v", sent)
	}
	if !strings.Contains(sent[0].Text, "test message") {
		t.Errorf("text = %q", sent[0].Text)
	}
}

func TestNotifyTestWithoutChannelsOrWithABrokenOne(t *testing.T) {
	w := newBackupWorld(t, "")
	if r := w.run("notify", "test"); r.code == 0 || !strings.Contains(r.stderr, "notify telegram") {
		t.Errorf("%+v", r)
	}
	server := w.withTelegram(t)
	server.refuse = true
	r := w.run("notify", "test")
	if r.code == 0 || !strings.Contains(r.stdout, "0 of 1") {
		t.Errorf("%+v", r)
	}
	noSecrets(t, "refused test", r.stdout, r.stderr)
}

func TestNotifyWebhookAndRemove(t *testing.T) {
	w := newBackupWorld(t, "")
	w.stdin = "https://example.org/hook/SECRET-PART\n"
	r := w.run("notify", "webhook", "--url-stdin")
	if r.code != 0 || strings.Contains(r.stdout+r.stderr, "SECRET-PART") {
		t.Fatalf("%+v", r)
	}
	if got, _ := w.store.Get(notify.KeyWebhookURL); !strings.Contains(got, "SECRET-PART") {
		t.Error("the address is not in the key store")
	}
	w.stdin = "ftp://nope\n"
	if r := w.run("notify", "webhook", "--url-stdin"); r.code == 0 || strings.Contains(r.stderr, "nope") {
		t.Errorf("%+v", r)
	}
	if r := w.run("notify", "remove", "webhook"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := w.store.Get(notify.KeyWebhookURL); err == nil {
		t.Error("the secret must go with the channel")
	}
	if r := w.run("notify", "remove", "pigeon"); r.code != exitUsage {
		t.Errorf("%+v", r)
	}
}

func TestAJobThatFailsIsReportedOnceAndSuccessIsNeverReported(t *testing.T) {
	w := newBackupWorld(t, "fails")
	server := w.withTelegram(t)

	r := w.run("run", "--job", "claude", "--json")
	if r.code == 0 {
		t.Fatal("the job must still fail")
	}
	sent := server.messages()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "'claude'") || !strings.Contains(sent[0].Text, "disk is on fire") {
		t.Fatalf("sent = %+v", sent)
	}
	noSecrets(t, "failure message", sent[0].Text, r.stdout, r.stderr)
	for _, record := range readRunLog(t) {
		data, _ := json.Marshal(record)
		noSecrets(t, "run log", string(data))
	}

	// The same failure at the next run is not news again.
	if r := w.run("run", "--job", "claude", "--json"); r.code == 0 {
		t.Fatal("still failing")
	}
	if got := len(server.messages()); got != 1 {
		t.Errorf("%d messages: a failure that goes on must not be repeated at every run", got)
	}
	// Another job has its own state (the projects job fails in this world too:
	// the fake home has no project of its own).
	if r := w.run("run", "--job", "projects", "--json"); r.code == 0 {
		t.Fatal("projects fails too in this world")
	}
	if got := len(server.messages()); got != 2 {
		t.Errorf("%d messages: another job's failure is news", got)
	}
}

func TestSuccessfulRunsSayNothingAndClearTheFailure(t *testing.T) {
	good := newBackupWorld(t, "")
	server := good.withTelegram(t)
	for _, job := range []string{"claude", "cleanup", "check"} {
		if r := good.run("run", "--job", job, "--json"); r.code != 0 {
			t.Fatalf("%s: %+v", job, r)
		}
	}
	if sent := server.messages(); len(sent) != 0 {
		t.Fatalf("successful runs must be silent: %+v", sent)
	}

	// A failure, then a success, then a failure: the second failure is news.
	layout, _ := config.Resolve()
	if err := watch.Update(layout.WatchFile(), func(s *watch.State) { s.JobTold("cleanup", time.Now()) }); err != nil {
		t.Fatal(err)
	}
	if r := good.run("run", "--job", "cleanup"); r.code != 0 {
		t.Fatal("cleanup works")
	}
	if state, _ := watch.Load(layout.WatchFile()); state.Jobs["cleanup"].Failing {
		t.Error("a success must clear the failure")
	}
}

func TestAnUndeliveredFailureIsTriedAgainNextTime(t *testing.T) {
	w := newBackupWorld(t, "fails")
	server := w.withTelegram(t)
	server.refuse = true
	r := w.run("run", "--job", "claude", "--json")
	if r.code == 0 {
		t.Fatal("the job must fail")
	}
	noSecrets(t, "undelivered", r.stdout, r.stderr)
	records := readRunLog(t)
	if len(records) != 1 || len(records[0].Notes) == 0 {
		t.Fatalf("the log must say that the message did not go: %+v", records)
	}
	for _, note := range records[0].Notes {
		noSecrets(t, "note", note)
	}

	server.refuse = false
	if r := w.run("run", "--job", "claude", "--json"); r.code == 0 {
		t.Fatal("still failing")
	}
	if got := len(server.messages()); got != 1 {
		t.Errorf("%d messages: once Telegram works again the failure must arrive", got)
	}
}

func TestAJobWithoutChannelsFailsQuietly(t *testing.T) {
	w := newBackupWorld(t, "fails")
	r := w.run("run", "--job", "claude", "--json")
	if r.code == 0 {
		t.Fatal("the job must fail")
	}
	records := readRunLog(t)
	if len(records) != 1 || records[0].OK {
		t.Fatalf("%+v", records)
	}
}

// addTranscripts puts n more sessions into the fake Claude folder.
func addTranscripts(t *testing.T, home string, n int) []string {
	t.Helper()
	var paths []string
	for i := 0; i < n; i++ {
		path := filepath.Join(home, ".claude", "projects", "-extra", fmt.Sprintf("session-%02d.jsonl", i))
		fakehome.Write(t, path, "{}\n")
		paths = append(paths, path)
	}
	return paths
}

func TestMassDeletionOfSessionsRaisesTheAlarm(t *testing.T) {
	w := newBackupWorld(t, "")
	server := w.withTelegram(t)
	paths := addTranscripts(t, w.home, 20)

	if r := w.run("run", "--job", "claude", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if sent := server.messages(); len(sent) != 0 {
		t.Fatalf("the first run has nothing to compare with: %+v", sent)
	}

	// 5 of about 21 files go: below the threshold of 20 percent.
	for _, path := range paths[:3] {
		_ = os.Remove(path)
	}
	if r := w.run("run", "--job", "claude", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if sent := server.messages(); len(sent) != 0 {
		t.Fatalf("a small loss is no alarm: %+v", sent)
	}

	// Now most of them go.
	for _, path := range paths[3:16] {
		_ = os.Remove(path)
	}
	r := w.run("run", "--job", "claude", "--json")
	if r.code != 0 {
		t.Fatalf("the backup itself must still run: %+v", r)
	}
	sent := server.messages()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "fewer Claude sessions") || !strings.Contains(sent[0].Text, "cleanupPeriodDays") {
		t.Fatalf("sent = %+v", sent)
	}
	noSecrets(t, "alarm", sent[0].Text)
	if calls := w.calls(t); len(calls) == 0 || !strings.HasPrefix(calls[len(calls)-1], `["backup"`) {
		t.Errorf("the snapshot must still be made after the alarm: %v", calls)
	}

	// The new count is the baseline: the same state is not an alarm again.
	if r := w.run("run", "--job", "claude", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if got := len(server.messages()); got != 1 {
		t.Errorf("%d messages: the alarm must not repeat every hour", got)
	}

	status := w.run("status", "--json")
	if !strings.Contains(status.stdout, "fewer Claude sessions") {
		t.Errorf("status must remember the alarm: %s", status.stdout)
	}
}

func TestTheDeletionThresholdIsASetting(t *testing.T) {
	w := newBackupWorld(t, "")
	server := w.withTelegram(t)
	changeConfig(t, func(cfg *config.Config) { cfg.Notify.DeletionDropPercent = 60 })
	paths := addTranscripts(t, w.home, 20)
	w.run("run", "--job", "claude")
	for _, path := range paths[:10] { // about half
		_ = os.Remove(path)
	}
	w.run("run", "--job", "claude")
	if sent := server.messages(); len(sent) != 0 {
		t.Fatalf("half is below 60 percent: %+v", sent)
	}
	for _, path := range paths[10:] {
		_ = os.Remove(path)
	}
	w.run("run", "--job", "claude")
	if sent := server.messages(); len(sent) != 1 {
		t.Fatalf("the rest is above 60 percent: %+v", sent)
	}
}

func TestAnUndeliveredDeletionAlarmIsRaisedAgain(t *testing.T) {
	w := newBackupWorld(t, "")
	server := w.withTelegram(t)
	paths := addTranscripts(t, w.home, 20)
	w.run("run", "--job", "claude")
	for _, path := range paths {
		_ = os.Remove(path)
	}
	server.refuse = true
	w.run("run", "--job", "claude")
	server.refuse = false
	w.run("run", "--job", "claude")
	if sent := server.messages(); len(sent) != 1 {
		t.Fatalf("the alarm must arrive once Telegram works: %+v", sent)
	}
}

func TestWithoutChannelsDeletionIsStillRecorded(t *testing.T) {
	w := newBackupWorld(t, "")
	paths := addTranscripts(t, w.home, 20)
	w.run("run", "--job", "claude")
	for _, path := range paths {
		_ = os.Remove(path)
	}
	if r := w.run("run", "--job", "claude"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	layout, _ := config.Resolve()
	state, _ := watch.Load(layout.WatchFile())
	if state.Transcripts == nil || state.Transcripts.Files >= 20 || len(state.Alerts) != 1 {
		t.Errorf("%+v", state)
	}
}

func TestAMissingImportantFolderRaisesTheAlarmOnce(t *testing.T) {
	w := newBackupWorld(t, "")
	server := w.withTelegram(t)
	if r := w.run("run", "--job", "claude"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if err := os.RemoveAll(filepath.Join(w.home, ".claude", "projects")); err != nil {
		t.Fatal(err)
	}
	w.run("run", "--job", "claude")
	sent := server.messages()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "folder is gone") || !strings.Contains(sent[0].Text, filepath.Join(".claude", "projects")) {
		t.Fatalf("sent = %+v", sent)
	}
	w.run("run", "--job", "claude")
	if got := len(server.messages()); got != 1 {
		t.Errorf("%d messages: a missing folder is reported once", got)
	}
}

func TestAComputerWithoutClaudeDesktopIsNotAlarming(t *testing.T) {
	w := newBackupWorld(t, "")
	w.home = fakehome.Copy(t, fakehome.NoDesktop)
	server := w.withTelegram(t)
	w.run("run", "--job", "claude")
	w.run("run", "--job", "claude")
	if sent := server.messages(); len(sent) != 0 {
		t.Fatalf("a folder that never existed is not lost: %+v", sent)
	}
}

func TestEveryNewOutputKeyExists(t *testing.T) {
	tr := i18n.New("en")
	for _, verdict := range []string{verdictOK, verdictAttention, verdictProblem} {
		for _, key := range []string{"status.verdict." + verdict, "notify.summary.title." + verdict} {
			if tr.T(key) == key {
				t.Errorf("no text for %s", key)
			}
		}
	}
	for _, issue := range []string{
		issueRepoUnreachable, issueNoSnapshots, issueStale, issueCheckErrors, issueCheckFailed,
		issueCheckOverdue, issueJobFailing, issueDiskLow, issueAlert, issueOffsiteBroken,
	} {
		if tr.T("status.issue."+issue) == "status.issue."+issue {
			t.Errorf("no text for issue %s", issue)
		}
	}
	for _, name := range append(profile.Names(), "telegram", "webhook") {
		if tr.T("status.profile."+name) == "status.profile."+name && tr.T("status.channel."+name) == "status.channel."+name {
			t.Errorf("no text for %s", name)
		}
	}
}
