package discover

import (
	"path/filepath"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/manifests"
)

func fakeEnv(t *testing.T) Env {
	t.Helper()
	home, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake-home"))
	if err != nil {
		t.Fatal(err)
	}
	return Env{Home: home, Lookup: func(name string) string {
		if name == "APPDATA" {
			return filepath.Join(home, "AppData", "Roaming")
		}
		return ""
	}}
}

func TestExpand(t *testing.T) {
	env := Env{Home: filepath.FromSlash("/home/user"), Lookup: func(name string) string {
		if name == "APPDATA" {
			return "base"
		}
		return ""
	}}
	if got, ok := env.Expand("~/.claude/projects"); !ok || got != filepath.FromSlash("/home/user/.claude/projects") {
		t.Errorf("home: %q %v", got, ok)
	}
	if got, ok := env.Expand("%APPDATA%/Claude"); !ok || got != filepath.FromSlash("base/Claude") {
		t.Errorf("variable: %q %v", got, ok)
	}
	if _, ok := env.Expand("%NOT_SET%/Claude"); ok {
		t.Error("unset variable accepted")
	}
}

func TestLocateFindsFakeClaude(t *testing.T) {
	manifest, err := manifests.Load("windows")
	if err != nil {
		t.Fatal(err)
	}
	found := Locate(manifest, fakeEnv(t))
	if !found.Home.Exists {
		t.Errorf("fake ~/.claude not found: %+v", found.Home)
	}
	if len(found.Desktop) != 1 || !found.Desktop[0].Exists {
		t.Errorf("fake Desktop folder not found: %+v", found.Desktop)
	}
}

func TestLocateWithoutClaude(t *testing.T) {
	manifest, err := manifests.Load("windows")
	if err != nil {
		t.Fatal(err)
	}
	found := Locate(manifest, Env{Home: t.TempDir(), Lookup: func(string) string { return "" }})
	if found.Home.Exists || len(found.Desktop) != 0 {
		t.Errorf("found something in an empty home: %+v", found)
	}
}
