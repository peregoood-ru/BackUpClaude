package manifests

import (
	"path"
	"slices"
	"strings"
	"testing"
)

func TestLoadEveryOS(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		manifest, err := Load(goos)
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		if manifest.OS != goos || len(manifest.Desktop.Roots) == 0 || len(manifest.Desktop.Markers) == 0 {
			t.Errorf("%s: %+v", goos, manifest)
		}
		for _, name := range []string{".credentials.json", "node_modules"} {
			if !slices.Contains(manifest.Never, name) {
				t.Errorf("%s: %s must never be saved", goos, name)
			}
		}
		if !slices.Contains(manifest.Claude.Exclude, ".credentials.json") {
			t.Errorf("%s: login tokens are not excluded", goos)
		}
	}
}

func TestLoadUnknownOS(t *testing.T) {
	if _, err := Load("plan9"); err == nil {
		t.Fatal("expected an error")
	}
}

// matchesAny applies an exclude list the way the backup does.
func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

func TestNothingIsBothIncludedAndExcluded(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		manifest, err := Load(goos)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range manifest.Claude.Include {
			if matchesAny(manifest.Claude.Exclude, name) {
				t.Errorf("%s: %s is included and excluded", goos, name)
			}
		}
		for _, name := range manifest.Desktop.Include {
			if matchesAny(manifest.Desktop.Exclude, name) {
				t.Errorf("%s: %s is included and excluded", goos, name)
			}
		}
	}
}

// TestWindowsManifestMatchesTheSpecification pins the table of what is saved
// and what is not. A change here is a change of what users lose or keep.
func TestWindowsManifestMatchesTheSpecification(t *testing.T) {
	manifest, err := Load("windows")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"projects", "file-history", "skills", "plugins", "settings.json", "settings.local.json", "history.jsonl"} {
		if !slices.Contains(manifest.Claude.Include, name) {
			t.Errorf("Claude Code: %s is missing", name)
		}
	}
	if !slices.Contains(manifest.Claude.Files, "~/.claude.json") {
		t.Error("~/.claude.json is missing")
	}
	for _, name := range []string{"claude-code-sessions", "local-agent-mode-sessions", "config.json", "claude_desktop_config.json", "Local Storage", "IndexedDB"} {
		if !slices.Contains(manifest.Desktop.Include, name) {
			t.Errorf("Desktop: %s is missing", name)
		}
	}
	for _, name := range []string{"telemetry", "sessions", "session-env", "shell-snapshots", "cache", ".credentials.json"} {
		if !matchesAny(manifest.Claude.Exclude, name) {
			t.Errorf("Claude Code: %s must stay out", name)
		}
	}
	for _, name := range []string{"vm_bundles", "claude-code", "claude-code-vm", "Partitions", "Cache", "Code Cache", "logs"} {
		if !matchesAny(manifest.Desktop.Exclude, name) {
			t.Errorf("Desktop: %s must stay out", name)
		}
	}
}

func TestWindowsManifestKnowsBothDesktopInstallations(t *testing.T) {
	manifest, err := Load("windows")
	if err != nil {
		t.Fatal(err)
	}
	roots := strings.Join(manifest.Desktop.Roots, " ")
	if !strings.Contains(roots, "Packages/*/LocalCache/Roaming/Claude") || !strings.Contains(roots, "%APPDATA%/Claude") {
		t.Errorf("roots = %v", manifest.Desktop.Roots)
	}
	// Outside the application's package %APPDATA%\Claude of a Store installation
	// does not exist, so the package path must win.
	if !strings.Contains(manifest.Desktop.Roots[0], "Packages") {
		t.Errorf("the package path must come first: %v", manifest.Desktop.Roots)
	}
}
