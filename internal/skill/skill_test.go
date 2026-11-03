package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderFillsTheCommand(t *testing.T) {
	text := Render(`"C:\Program Files\x\backupclaude.exe"`)
	if strings.Contains(text, "{{") {
		t.Error("a placeholder is left")
	}
	if !strings.HasPrefix(text, "---\nname: backup\n") {
		t.Errorf("the front matter is wrong:\n%s", text[:80])
	}
	if !strings.Contains(text, `"C:\Program Files\x\backupclaude.exe" status --json`) {
		t.Error("the command is not in the examples")
	}
	for _, needed := range []string{"status --json", "list --sessions", "restore --session", "--apply --dry-run", "Never run `password --show`"} {
		if !strings.Contains(text, needed) {
			t.Errorf("the skill does not mention %q", needed)
		}
	}
}

func TestCommand(t *testing.T) {
	if Command(`C:\a b\backupclaude.exe`, true) != "backupclaude" {
		t.Error("on PATH")
	}
	if Command(`C:\a b\backupclaude.exe`, false) != `"C:\a b\backupclaude.exe"` {
		t.Error("full path")
	}
}

func TestInstallStatusRemove(t *testing.T) {
	home := t.TempDir()
	state, err := Status(home, "backupclaude")
	if err != nil || state.Installed {
		t.Fatalf("%+v %v", state, err)
	}
	if changed, err := Install(home, "backupclaude"); err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	if changed, err := Install(home, "backupclaude"); err != nil || changed {
		t.Fatalf("second install: %v %v", changed, err)
	}
	state, _ = Status(home, "backupclaude")
	if !state.Installed || !state.Ours || !state.Current {
		t.Errorf("%+v", state)
	}
	// A new command rewrites the file.
	if changed, err := Install(home, `"D:\new\backupclaude.exe"`); err != nil || !changed {
		t.Fatalf("rewrite: %v %v", changed, err)
	}
	if removed, err := Remove(home); err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if _, err := os.Stat(Dir(home)); err == nil {
		t.Error("the empty folder stayed")
	}
	if removed, err := Remove(home); err != nil || removed {
		t.Errorf("removing nothing: %v %v", removed, err)
	}
}

func TestAForeignSkillIsNeverTouched(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(Dir(home), "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: backup\n---\nmy own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(home, "backupclaude"); err == nil {
		t.Error("overwrote a foreign skill")
	}
	if _, err := Remove(home); err == nil {
		t.Error("removed a foreign skill")
	}
	if data, _ := os.ReadFile(path); string(data) != "---\nname: backup\n---\nmy own\n" {
		t.Errorf("the file changed: %q", data)
	}
}
