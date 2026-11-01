//go:build windows

package config

import (
	"os"
	"path/filepath"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

func osDataDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", i18n.E("config.datadir.unknown", "LOCALAPPDATA").Hint("config.datadir.unknown.hint", EnvHome)
	}
	return filepath.Join(base, appDirName), nil
}
