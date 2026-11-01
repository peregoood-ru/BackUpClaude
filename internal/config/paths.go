package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

const (
	// EnvHome overrides the data directory, for tests and portable setups.
	EnvHome = "BACKUPCLAUDE_HOME"
	// FileName is the settings file inside the data directory.
	FileName = "config.json"

	appDirName = "BackUpClaude"
)

// Layout names the paths inside the data directory.
type Layout struct {
	Root string
}

// ConfigFile returns the path of config.json.
func (l Layout) ConfigFile() string { return filepath.Join(l.Root, FileName) }

// ProjectsFile remembers the projects found in transcripts.
func (l Layout) ProjectsFile() string { return filepath.Join(l.Root, "projects.json") }

// BinDir holds restic and rclone.
func (l Layout) BinDir() string { return filepath.Join(l.Root, "bin") }

// LogsDir holds the logs.
func (l Layout) LogsDir() string { return filepath.Join(l.Root, "logs") }

// CacheDir holds the restic cache.
func (l Layout) CacheDir() string { return filepath.Join(l.Root, "cache") }

// TempDir holds short-lived work folders.
func (l Layout) TempDir() string { return filepath.Join(l.Root, "tmp") }

// Resolve finds the data directory by the rules of the OS. It creates nothing.
func Resolve() (Layout, error) {
	if override := strings.TrimSpace(os.Getenv(EnvHome)); override != "" {
		if !filepath.IsAbs(override) {
			return Layout{}, i18n.E("config.datadir.relative", EnvHome, override).Hint("config.datadir.relative.hint", EnvHome)
		}
		return Layout{Root: filepath.Clean(override)}, nil
	}
	root, err := osDataDir()
	if err != nil {
		return Layout{}, err
	}
	return Layout{Root: root}, nil
}
