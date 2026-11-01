package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// keyringService groups the application's entries in the OS key store:
// Credential Manager on Windows, Keychain on macOS, Secret Service on Linux.
const keyringService = "BackUpClaude"

// Keyring is the Store backed by the OS key store.
type Keyring struct{}

// NewKeyring returns the OS-backed Store.
func NewKeyring() Keyring { return Keyring{} }

// Get implements Store.
func (Keyring) Get(key string) (string, error) {
	value, err := keyring.Get(keyringService, key)
	return value, translate(err)
}

// Set implements Store.
func (Keyring) Set(key, value string) error {
	return translate(keyring.Set(keyringService, key, value))
}

// Delete implements Store.
func (Keyring) Delete(key string) error {
	return translate(keyring.Delete(keyringService, key))
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
