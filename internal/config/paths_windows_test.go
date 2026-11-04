//go:build windows

package config

import (
	"path/filepath"
	"testing"
)

const dataDirName = windowsDirName

// The data folder must not follow %LOCALAPPDATA%: a Store installation of
// Claude Desktop redirects that folder into its package.
func TestWindowsDataDirIsInTheProfileRoot(t *testing.T) {
	profile := t.TempDir()
	t.Setenv(EnvHome, "")
	t.Setenv("USERPROFILE", profile)
	t.Setenv("LOCALAPPDATA", filepath.Join(profile, "AppData", "Local"))
	layout, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(profile, ".backupclaude"); layout.Root != want {
		t.Errorf("root = %q, want %q", layout.Root, want)
	}
}
