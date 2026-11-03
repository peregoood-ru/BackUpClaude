package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/sandbox"
)

// keyringService groups the application's entries in the OS key store:
// Credential Manager on Windows, Keychain on macOS, Secret Service on Linux.
const keyringService = "BackUpClaude"

// service is the group of entries in use: a sandbox has its own, so that trying
// the application never reads or replaces the real password.
func service() string { return keyringService + sandbox.Suffix() }

// Keyring is the Store backed by the OS key store.
type Keyring struct{}

// NewKeyring returns the OS-backed Store.
func NewKeyring() Keyring { return Keyring{} }

// Get implements Store.
func (Keyring) Get(key string) (string, error) {
	value, err := keyring.Get(service(), key)
	return value, translate(err)
}

// Set implements Store.
func (Keyring) Set(key, value string) error {
	return translate(keyring.Set(service(), key, value))
}

// Delete implements Store.
func (Keyring) Delete(key string) error {
	return translate(keyring.Delete(service(), key))
}

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return ErrNotFound
	default:
		return i18n.E("secrets.unavailable").Hint("secrets.unavailable.hint").Because(err)
	}
}
