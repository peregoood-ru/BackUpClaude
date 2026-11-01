//go:build darwin

package config

import (
	"os"
	"path/filepath"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

func osDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", i18n.E("config.datadir.unknown", "HOME").Hint("config.datadir.unknown.hint", EnvHome)
	}
	return filepath.Join(home, "Library", "Application Support", appDirName), nil
}
