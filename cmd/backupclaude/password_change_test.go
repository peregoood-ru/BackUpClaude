package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

// localRepoURL is the local copy of the world being tested.
var localRepoURL string

const (
	oldPassword = "OLD0-OLD0-OLD0-OLD0-OLD0-OLD0-OLD0-OLD0"
	extRepoURL  = "sftp:ext.example:/copy"
	offlineURL  = "sftp:offline.example:/copy"
)

// fakeKey is one key of a fake repository.
type fakeKey struct {
	ID       string `json:"id"`
	Password string `json:"password"`
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9]`)

func fakeKeyFile(dir, repo string) string {
	return filepath.Join(dir, "keys-"+nonWord.ReplaceAllString(repo, "_")+".json")
}

func loadFakeKeys(dir, repo string) []fakeKey {
	var keys []fakeKey
	data, err := os.ReadFile(fakeKeyFile(dir, repo))
	if err == nil {
		_ = json.Unmarshal(data, &keys)
	}
	return keys
}

func saveFakeKeys(dir, repo string, keys []fakeKey) {
	data, _ := json.Marshal(keys)
	_ = os.WriteFile(fakeKeyFile(dir, repo), data, 0o600)
}

func failMarker(dir, name string) string { return filepath.Join(dir, name) }

// fakeKeys plays restic for the commands the password change uses. Every
// repository is a list of keys; the password comes on standard input, or from
// the file taskpw when restic is given a password command (the stored password).
func fakeKeys(dir string) {
	args := os.Args[1:]
	record, _ := json.Marshal(args)
	logCall(dir, string(record))

	repo, passwordCommand, newFile := "", false, ""
	for i, arg := range args {
		switch arg {
		case "--repo":
			repo = args[i+1]
		case "--password-command":
			passwordCommand = true
		case "--new-password-file":
			newFile = args[i+1]
		}
	}
	fail := func(code int, message string) {
		fmt.Fprintf(os.Stderr, `{"message_type":"exit_error","code":%d,"message":%q}`+"\n", code, message)
		os.Exit(code)
	}
	if strings.HasPrefix(repo, "sftp:offline") {
		fail(1, "unable to open repository: connection refused")
	}
	var password string
	if passwordCommand {
		data, _ := os.ReadFile(filepath.Join(dir, "taskpw"))
		password = strings.TrimSpace(string(data))
	} else {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		password = strings.TrimRight(line, "\r\n")
	}
	keys := loadFakeKeys(dir, repo)
	current := -1
	for i, key := range keys {
		if key.Password == password {
			current = i
		}
	}
	if current < 0 {
		fail(12, "wrong password or no key found")
	}
	switch args[0] {
	case "cat":
	case "key":
		sub := ""
		for _, arg := range args[1:] {
			if arg == "list" || arg == "add" || arg == "remove" {
				sub = arg
				break
			}
		}
		switch sub {
		case "list":
			type entry struct {
				Current bool   `json:"current"`
				ID      string `json:"id"`
			}
			list := []entry{}
			for i, key := range keys {
				list = append(list, entry{i == current, key.ID})
			}
			data, _ := json.Marshal(list)
			fmt.Println(string(data))
		case "add":
			if _, err := os.Stat(failMarker(dir, "fail-add")); err == nil && strings.HasPrefix(repo, "sftp:ext") {
				fail(1, "cannot save key")
			}
			data, err := os.ReadFile(newFile)
			if err != nil {
				fail(1, "no password file")
			}
			keys = append(keys, fakeKey{ID: fmt.Sprintf("key%d", len(keys)+1) + fmt.Sprint(len(data)), Password: strings.TrimSpace(string(data))})
			saveFakeKeys(dir, repo, keys)
			fmt.Println("saved new key with ID", keys[len(keys)-1].ID)
		case "remove":
			if _, err := os.Stat(failMarker(dir, "fail-remove")); err == nil && strings.HasPrefix(repo, "sftp:ext") {
				fail(1, "cannot remove key")
			}
			id := args[len(args)-1]
			if keys[current].ID == id {
				fail(1, "refusing to remove key currently used to access repository")
			}
			var kept []fakeKey
			for _, key := range keys {
				if key.ID != id {
					kept = append(kept, key)
				}
			}
			if len(kept) == len(keys) {
				fail(1, "key not found")
			}
			saveFakeKeys(dir, repo, kept)
		}
	}
}

func logCall(dir, line string) {
	file, _ := os.OpenFile(filepath.Join(dir, "calls.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintln(file, line)
	_ = file.Close()
}

// watchedStore records every change of the key store in the order of the restic
// calls, and keeps the password the fake restic hands out as the stored one.
type watchedStore struct {
	*secrets.Memory
	dir string
}

func (s watchedStore) Set(key, value string) error {
	logCall(s.dir, "STORE set "+key)
	if key == secrets.RepoPasswordKey {
		_ = os.WriteFile(filepath.Join(s.dir, "taskpw"), []byte(value), 0o600)
	}
	return s.Memory.Set(key, value)
}

func (s watchedStore) Delete(key string) error {
	logCall(s.dir, "STORE delete "+key)
	return s.Memory.Delete(key)
}

type changeWorld struct {
	*backupWorld
	store watchedStore
}

// newChangeWorld is a computer with a local copy, and an external one when ext
// is set, each holding a single key with the old password.
func newChangeWorld(t *testing.T, ext string) *changeWorld {
	t.Helper()
	w := newBackupWorld(t, "keys")
	localRepoURL = filepath.Join(t.TempDir(), "repo")
	saveConfig(t, func(c *config.Config) {
		c.Repositories.Local = localRepoURL
		c.Repositories.Offsite = ext
	})
	store := watchedStore{Memory: w.world.store, dir: w.dir}
	if err := w.world.store.Set(secrets.RepoPasswordKey, oldPassword); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(w.dir, "taskpw"), []byte(oldPassword), 0o600)
	w.world.override = store
	w.terminal = true
	w.stdin = "yes\n"
	saveFakeKeys(w.dir, localRepoURL, []fakeKey{{"old1", oldPassword}})
	if ext != "" && !strings.HasPrefix(ext, "sftp:offline") {
		saveFakeKeys(w.dir, ext, []fakeKey{{"old2", oldPassword}})
	}
	return &changeWorld{w, store}
}

func (w *changeWorld) keys(repo string) []fakeKey { return loadFakeKeys(w.dir, repo) }

func (w *changeWorld) entry(t *testing.T, key string) string {
	t.Helper()
	value, _ := optionalSecret(w.store, key)
	return value
}

var passwordShape = regexp.MustCompile(`[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){7}`)

// shownPassword returns the one password the command printed.
func shownPassword(t *testing.T, out string) string {
	t.Helper()
	found := passwordShape.FindAllString(out, -1)
	if len(found) != 1 {
		t.Fatalf("expected one password on screen, found %d:\n%s", len(found), out)
	}
	return found[0]
}

func (w *changeWorld) assertUntouched(t *testing.T) {
	t.Helper()
	if got := w.entry(t, secrets.RepoPasswordKey); got != oldPassword {
		t.Errorf("stored password changed")
	}
	for _, key := range []string{secrets.PreviousPasswordKey, secrets.NextPasswordKey} {
		if w.entry(t, key) != "" {
			t.Errorf("leftover entry %s", key)
		}
	}
	for _, repo := range []string{localRepoURL, extRepoURL} {
		if keys := w.keys(repo); len(keys) > 1 || (len(keys) == 1 && keys[0].Password != oldPassword) {
			t.Errorf("%s changed: %+v", repo, keys)
		}
	}
}

func TestPasswordChangeNeedsATerminal(t *testing.T) {
	w := newChangeWorld(t, extRepoURL)
	w.terminal = false
	for _, args := range [][]string{{"password", "change"}, {"password", "change", "--json"}} {
		if r := w.run(args...); r.code == 0 || strings.Contains(r.stdout+r.stderr, oldPassword) {
			t.Errorf("%v: %+v", args, r)
		}
	}
	w.terminal = true
	if r := w.run("password", "change", "--json"); r.code == 0 {
		t.Errorf("--json must refuse: %+v", r)
	}
	w.assertUntouched(t)
	if calls := w.allCalls(t); len(calls) != 0 {
		t.Errorf("restic was called: %v", calls)
	}
}

func TestPasswordChangeAsksForYes(t *testing.T) {
	w := newChangeWorld(t, extRepoURL)
	for _, answer := range []string{"no\n", "\n", ""} {
		w.stdin = answer
		if r := w.run("password", "change"); r.code == 0 {
			t.Errorf("answer %q went through: %+v", answer, r)
		}
	}
	w.assertUntouched(t)
	if calls := w.allCalls(t); len(calls) != 0 {
		t.Errorf("restic was called before the confirmation: %v", calls)
	}
}

func TestPasswordChangeBothCopies(t *testing.T) {
	w := newChangeWorld(t, extRepoURL)
	r := w.run("password", "change")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	fresh := shownPassword(t, r.stdout)
	if fresh == oldPassword || w.entry(t, secrets.RepoPasswordKey) != fresh {
		t.Fatal("the key store does not hold the password that was shown")
	}
	for _, key := range []string{secrets.PreviousPasswordKey, secrets.NextPasswordKey} {
		if w.entry(t, key) != "" {
			t.Errorf("temporary entry %s was not deleted", key)
		}
	}
	for _, repo := range []string{localRepoURL, extRepoURL} {
		keys := w.keys(repo)
		if len(keys) != 1 || keys[0].Password != fresh {
			t.Errorf("%s: %+v", repo, keys)
		}
	}
	if !strings.Contains(r.stdout, "keys left: 1") || strings.Contains(r.stdout+r.stderr, oldPassword) {
		t.Errorf("output:\n%s", r.stdout)
	}

	// The order that keeps a copy from ever being left without a password.
	calls := strings.Join(w.allCalls(t), "\n")
	for _, secret := range []string{oldPassword, fresh} {
		if strings.Contains(calls, secret) {
			t.Error("a password reached a restic argument")
		}
	}
	order := []string{
		"STORE set " + secrets.NextPasswordKey,
		`"add"`, `"add"`,
		"STORE set " + secrets.PreviousPasswordKey,
		"STORE set " + secrets.RepoPasswordKey,
		`"remove"`, `"remove"`,
		"STORE delete " + secrets.NextPasswordKey,
		"STORE delete " + secrets.PreviousPasswordKey,
	}
	position := 0
	for _, want := range order {
		next := strings.Index(calls[position:], want)
		if next < 0 {
			t.Fatalf("%q is out of order or missing in:\n%s", want, calls)
		}
		position += next + len(want)
	}
	// The copies were also opened the way a scheduled task opens them.
	if !strings.Contains(calls, "--password-command") {
		t.Error("the stored password was never tried")
	}
	// No file with the password is left behind.
	layout, _ := config.Resolve()
	if left, _ := filepath.Glob(filepath.Join(layout.TempDir(), "pw-*")); len(left) != 0 {
		t.Errorf("password files left: %v", left)
	}
}

func TestPasswordChangeLocalOnly(t *testing.T) {
	w := newChangeWorld(t, "")
	r := w.run("password", "change")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	fresh := shownPassword(t, r.stdout)
	if keys := w.keys(localRepoURL); len(keys) != 1 || keys[0].Password != fresh {
		t.Errorf("%+v", keys)
	}
}

func TestPasswordChangeInRussian(t *testing.T) {
	w := newChangeWorld(t, "")
	t.Setenv("BACKUPCLAUDE_LANG", "ru")
	w.stdin = "да\n"
	r := w.run("password", "change")
	if r.code != 0 || !strings.Contains(r.stdout, "Пароль изменён") {
		t.Fatalf("%+v", r)
	}
	w.stdin = "нет\n"
	if r := w.run("password", "change"); r.code == 0 || !strings.Contains(r.stderr, "Отменено") {
		t.Errorf("%+v", r)
	}
}

// An external copy that cannot be reached stops the command before anything is
// changed, the local copy included.
func TestPasswordChangeStopsWhenExternalCopyIsOffline(t *testing.T) {
	w := newChangeWorld(t, offlineURL)
	r := w.run("password", "change")
	if r.code == 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stderr, "external copy") || !strings.Contains(r.stderr, "Nothing was changed") {
		t.Errorf("the message does not explain what to do:\n%s", r.stderr)
	}
	w.assertUntouched(t)
	for _, call := range w.allCalls(t) {
		if strings.Contains(call, `"add"`) || strings.Contains(call, `"remove"`) {
			t.Errorf("a key was touched: %s", call)
		}
	}
}

// A key that could not be added to the external copy is taken back from the
// local one, so that no key with a password nobody knows is left.
func TestPasswordChangeRollsBackWhenAddingFails(t *testing.T) {
	w := newChangeWorld(t, extRepoURL)
	if err := os.WriteFile(failMarker(w.dir, "fail-add"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := w.run("password", "change")
	if r.code == 0 {
		t.Fatalf("%+v", r)
	}
	if passwordShape.MatchString(r.stdout + r.stderr) {
		t.Error("a password appeared although the change failed")
	}
	w.assertUntouched(t)
}

// A stop after the key store was changed leaves both passwords working and
// stored, and the next run finishes with the same new password.
func TestPasswordChangeResumes(t *testing.T) {
	w := newChangeWorld(t, extRepoURL)
	if err := os.WriteFile(failMarker(w.dir, "fail-remove"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := w.run("password", "change")
	if r.code == 0 {
		t.Fatalf("%+v", r)
	}
	if passwordShape.MatchString(r.stdout + r.stderr) {
		t.Error("a password appeared although the change is not finished")
	}
	if !strings.Contains(r.stderr, "Run backupclaude password change again") {
		t.Errorf("no instruction:\n%s", r.stderr)
	}
	fresh := w.entry(t, secrets.RepoPasswordKey)
	if fresh == oldPassword || w.entry(t, secrets.PreviousPasswordKey) != oldPassword || w.entry(t, secrets.NextPasswordKey) != fresh {
		t.Fatal("the key store does not hold both passwords")
	}
	if len(w.keys(extRepoURL)) != 2 || len(w.keys(localRepoURL)) != 1 {
		t.Fatalf("local %+v, external %+v", w.keys(localRepoURL), w.keys(extRepoURL))
	}

	if err := os.Remove(failMarker(w.dir, "fail-remove")); err != nil {
		t.Fatal(err)
	}
	r = w.run("password", "change")
	if r.code != 0 || !strings.Contains(r.stdout, "stopped halfway") {
		t.Fatalf("%+v", r)
	}
	if shownPassword(t, r.stdout) != fresh {
		t.Error("the finished change shows another password")
	}
	for _, repo := range []string{localRepoURL, extRepoURL} {
		if keys := w.keys(repo); len(keys) != 1 || keys[0].Password != fresh {
			t.Errorf("%s: %+v", repo, keys)
		}
	}
	for _, key := range []string{secrets.PreviousPasswordKey, secrets.NextPasswordKey} {
		if w.entry(t, key) != "" {
			t.Errorf("temporary entry %s was not deleted", key)
		}
	}
}

// A run that stopped after the new key was added, before the key store was
// touched, is finished from the saved new password.
func TestPasswordChangeResumesBeforeTheSwap(t *testing.T) {
	w := newChangeWorld(t, "")
	fresh, err := secrets.GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.store.Memory.Set(secrets.NextPasswordKey, fresh); err != nil {
		t.Fatal(err)
	}
	saveFakeKeys(w.dir, localRepoURL, []fakeKey{{"old1", oldPassword}, {"new1", fresh}})
	r := w.run("password", "change")
	if r.code != 0 || shownPassword(t, r.stdout) != fresh {
		t.Fatalf("%+v", r)
	}
	if keys := w.keys(localRepoURL); len(keys) != 1 || keys[0].Password != fresh {
		t.Errorf("%+v", keys)
	}
}

func TestPasswordChangeRefusesWhenStoredPasswordDoesNotFit(t *testing.T) {
	w := newChangeWorld(t, extRepoURL)
	saveFakeKeys(w.dir, extRepoURL, []fakeKey{{"other", "SOMETHING-ELSE"}})
	r := w.run("password", "change")
	if r.code == 0 || !strings.Contains(r.stderr, "external copy") {
		t.Fatalf("%+v", r)
	}
	if got := w.entry(t, secrets.RepoPasswordKey); got != oldPassword || w.entry(t, secrets.NextPasswordKey) != "" {
		t.Error("the key store was changed")
	}
	if keys := w.keys(localRepoURL); len(keys) != 1 {
		t.Errorf("local copy changed: %+v", keys)
	}
}

func TestPasswordChangeNeedsAStoredPassword(t *testing.T) {
	w := newChangeWorld(t, "")
	_ = w.store.Memory.Delete(secrets.RepoPasswordKey)
	if r := w.run("password", "change"); r.code == 0 {
		t.Fatalf("%+v", r)
	}
}
