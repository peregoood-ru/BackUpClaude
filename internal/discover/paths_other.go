//go:build !windows

package discover

import (
	"path/filepath"
	"strings"
)

// normalizePath brings a path from a transcript to one spelling. It returns
// false for a path that does not belong to this OS.
func normalizePath(raw string) (string, bool) {
	path := strings.TrimSpace(raw)
	if !filepath.IsAbs(path) {
		return "", false
	}
	return filepath.Clean(path), true
}

// pathKey makes paths comparable.
func pathKey(path string) string { return path }
