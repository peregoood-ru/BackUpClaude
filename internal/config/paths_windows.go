//go:build windows

package config

import (
	"os"
	"path/filepath"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// windowsDirName is the data folder inside the user profile. It is not under
// %LOCALAPPDATA%: a Store installation of Claude Desktop redirects that folder
// into its package for everything it starts, and a scheduled task, which runs
// outside the package, would not see it. The profile root is not redirected.
const windowsDirName = ".backupclaude"

func osDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", i18n.E("config.datadir.unknown", "USERPROFILE").Hint("config.datadir.unknown.hint", EnvHome)
	}
	return filepath.Join(home, windowsDirName), nil
}
