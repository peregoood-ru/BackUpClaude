// Package skill installs the /backup skill of Claude Code. The skill is a short
// text that tells Claude which commands of the application to run, so that a
// person can ask Claude to check the backups or bring a session back.
package skill

import (
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Name is the name of the skill and of its folder.
const Name = "backup"

// marker is on the second line of a skill this program wrote. A skill without
// it is the user's own and is never replaced or removed.
const marker = "<!-- managed by BackUpClaude."

const fileName = "SKILL.md"

//go:embed SKILL.md
var template string

// Render fills in the command the skill tells Claude to run.
func Render(command string) string {
	return strings.ReplaceAll(template, "{{COMMAND}}", command)
}

// Command returns how the skill should call the program: by name when it is on
// the PATH, otherwise by its full path in quotes.
func Command(executable string, onPath bool) string {
	if onPath {
		return "backupclaude"
	}
	return `"` + executable + `"`
}

// Dir is the folder of the skill inside Claude's home folder.
func Dir(claudeHome string) string { return filepath.Join(claudeHome, "skills", Name) }

// State is what is installed.
type State struct {
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	// Ours is true when this program wrote the file.
	Ours bool `json:"ours"`
	// Current is true when the text equals what a new install would write.
	Current bool `json:"current"`
}

// Status reads what is installed.
func Status(claudeHome, command string) (State, error) {
	path := filepath.Join(Dir(claudeHome), fileName)
	state := State{Path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, i18n.E("skill.read_failed", path).Because(err)
	}
	state.Installed = true
	state.Ours = strings.Contains(string(data), marker)
	state.Current = string(data) == Render(command)
	return state, nil
}

// Install writes the skill. It reports whether it wrote anything. A skill with
// the same name that this program did not write stays untouched.
func Install(claudeHome, command string) (changed bool, err error) {
	state, err := Status(claudeHome, command)
	if err != nil {
		return false, err
	}
	if state.Installed && !state.Ours {
		return false, i18n.E("skill.foreign", state.Path).Hint("skill.foreign.hint")
	}
	if state.Current {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(state.Path), 0o755); err != nil {
		return false, i18n.E("skill.write_failed", state.Path).Because(err)
	}
	if err := os.WriteFile(state.Path, []byte(Render(command)), 0o644); err != nil {
		return false, i18n.E("skill.write_failed", state.Path).Because(err)
	}
	return true, nil
}

// Remove deletes the skill this program wrote and its folder if it is empty.
// It reports whether anything was removed.
func Remove(claudeHome string) (removed bool, err error) {
	state, err := Status(claudeHome, "")
	if err != nil {
		return false, err
	}
	if !state.Installed {
		return false, nil
	}
	if !state.Ours {
		return false, i18n.E("skill.foreign", state.Path).Hint("skill.foreign.hint")
	}
	if err := os.Remove(state.Path); err != nil {
		return false, i18n.E("skill.write_failed", state.Path).Because(err)
	}
	_ = os.Remove(filepath.Dir(state.Path))
	return true, nil
}
