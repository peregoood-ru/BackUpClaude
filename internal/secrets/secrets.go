// Package secrets keeps the repository password in the OS key store. The value
// never goes to a file, a log or an argument list.
package secrets

import (
	"crypto/rand"
	"errors"
	"strings"
	"sync"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// RepoPasswordKey names the repository password in the key store.
const RepoPasswordKey = "repository-password"

// Two more entries exist only while the password is being changed. Previous
// holds the old password until the old restic key is gone, and Next holds the
// new one from before it is added to a repository until the change is done, so
// that a change that stopped halfway can be finished and nothing is lost.
const (
	PreviousPasswordKey = "repository-password-previous"
	NextPasswordKey     = "repository-password-next"
)

// ErrNotFound means the key store has no such entry.
var ErrNotFound = errors.New("secret not found")

// Store is a place for secrets.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Crockford base32 without I, L, O, U: nothing to misread on paper.
const passwordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const (
	passwordGroups    = 8
	passwordGroupSize = 4
)

// GeneratePassword returns 160 random bits as groups of characters that are
// easy to copy to paper by hand.
func GeneratePassword() (string, error) {
	raw := make([]byte, passwordGroups*passwordGroupSize)
	if _, err := rand.Read(raw); err != nil {
		return "", i18n.E("secrets.random_failed").Because(err)
	}
	var b strings.Builder
	for i, value := range raw {
		if i > 0 && i%passwordGroupSize == 0 {
			b.WriteByte('-')
		}
		// 256 is a multiple of 32, so the mask keeps the choice uniform.
		b.WriteByte(passwordAlphabet[value&31])
	}
	return b.String(), nil
}

// EnsureRepoPassword creates the repository password if there is none and
// reports whether it did. An existing password is never replaced: that would
// lock the user out of the repository.
func EnsureRepoPassword(s Store) (created bool, err error) {
	if _, err := s.Get(RepoPasswordKey); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	password, err := GeneratePassword()
	if err != nil {
		return false, err
	}
	if err := s.Set(RepoPasswordKey, password); err != nil {
		return false, err
	}
	stored, err := s.Get(RepoPasswordKey)
	if err != nil {
		return false, err
	}
	if stored != password {
		return false, i18n.E("secrets.verify_failed").Hint("secrets.unavailable.hint")
	}
	return true, nil
}

// Memory is an in-process Store for tests.
type Memory struct {
	mu     sync.Mutex
	values map[string]string
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{values: map[string]string{}}
}

// Get implements Store.
func (m *Memory) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[key]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

// Set implements Store.
func (m *Memory) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

// Delete implements Store.
func (m *Memory) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.values[key]; !ok {
		return ErrNotFound
	}
	delete(m.values, key)
	return nil
}
