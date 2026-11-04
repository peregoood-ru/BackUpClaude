//go:build !windows

package secrets

import "os"

// restrictToUser leaves the file to its owner: mode 0600.
func restrictToUser(path string) error { return os.Chmod(path, 0o600) }
