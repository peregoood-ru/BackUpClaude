//go:build !windows && !darwin

package config

import (
	"os"
	"path/filepath"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

func osDataDir() (string, error) {
	if base := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(base) {
		return filepath.Join(base, appDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", i18n.E("config.datadir.unknown", "HOME").Hint("config.datadir.unknown.hint", EnvHome)
	}
	return filepath.Join(home, ".local", "share", appDirName), nil
}
