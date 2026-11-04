package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestGeneratePasswordShape(t *testing.T) {
	shape := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){7}$`)
	seen := map[string]bool{}
	for range 100 {
		password, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if !shape.MatchString(password) {
			t.Fatal("password has the wrong shape")
		}
		if seen[password] {
			t.Fatal("password repeated")
		}
		seen[password] = true
	}
}

func TestEnsureRepoPasswordCreatesOnce(t *testing.T) {
	store := NewMemory()
	created, err := EnsureRepoPassword(store)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	first, err := store.Get(RepoPasswordKey)
	if err != nil {
		t.Fatal(err)
	}
	created, err = EnsureRepoPassword(store)
	if err != nil || created {
		t.Fatalf("second call: created=%v err=%v", created, err)
	}
	if second, _ := store.Get(RepoPasswordKey); second != first {
		t.Fatal("existing password was replaced")
	}
}

type brokenStore struct{ Memory }

func (*brokenStore) Get(string) (string, error) { return "", errors.New("store is down") }

func TestEnsureRepoPasswordDoesNotHideFailures(t *testing.T) {
	if created, err := EnsureRepoPassword(&brokenStore{}); err == nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
}

type lossyStore struct{ *Memory }

func (l lossyStore) Set(key, value string) error { return l.Memory.Set(key, value+"x") }

func TestEnsureRepoPasswordVerifiesWhatWasStored(t *testing.T) {
	if _, err := EnsureRepoPassword(lossyStore{NewMemory()}); err == nil {
		t.Fatal("a corrupted write went unnoticed")
	}
}

func TestMemoryDelete(t *testing.T) {
	store := NewMemory()
	if err := store.Delete("absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := store.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestKeyringUsesMockedBackend(t *testing.T) {
	keyring.MockInit()
	store := NewKeyring()
	if _, err := store.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing entry: %v", err)
	}
	if err := store.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if value, err := store.Get("k"); err != nil || value != "v" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if err := store.Delete("k"); err != nil {
		t.Fatal(err)
	}
}

func TestKeyringReportsUnavailableBackend(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	defer keyring.MockInit()
	_, err := NewKeyring().Get("k")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestWriteTempHoldsSecretUntilRemoved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp")
	path, remove, err := WriteTemp(dir, "SECRET-VALUE")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != "SECRET-VALUE" {
		t.Fatalf("file holds %q, %v", data, err)
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still there: %v", err)
	}
	remove() // calling it twice is harmless
}
