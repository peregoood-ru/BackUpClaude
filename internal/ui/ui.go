// Package ui serves the local browser page. The page holds no logic: every
// button runs the same command as the command line does (status, list, backup,
// restore, schedule status), in this process, and shows its --json answer. The
// server lives while the page is open and ends when it is closed, so no
// background process is left.
package ui

import (
	"context"
	"encoding/json"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Reply is the answer of a command: its exit code and the --json envelope it
// printed.
type Reply struct {
	Code int
	Body []byte
}

// Backend is what the page needs from the application. Run executes a command
// with --json; the arguments are always built by this package from fixed
// templates, never taken from the page.
type Backend interface {
	Run(ctx context.Context, args []string) Reply
	// Settings reads the settings for the page. It never carries a secret.
	Settings(ctx context.Context) (Settings, error)
	// Save applies the changes of the settings page and returns the new state.
	Save(ctx context.Context, patch Patch) (Settings, error)
}

// Settings is the settings page. The repository password is not part of it:
// the page only learns whether one is stored.
type Settings struct {
	Language       string           `json:"language"`
	DataFolder     string           `json:"data_folder"`
	Local          string           `json:"local"`
	Offsite        string           `json:"offsite"`
	Schedule       config.Schedule  `json:"schedule"`
	Retention      config.Retention `json:"retention"`
	DropPercent    int              `json:"deletion_drop_percent"`
	Extra          []string         `json:"extra"`
	Exclude        []string         `json:"exclude"`
	Channels       []string         `json:"channels"`
	Profiles       config.Profiles  `json:"profiles"`
	Tray           bool             `json:"tray"`
	TraySupported  bool             `json:"tray_supported"`
	PasswordStored bool             `json:"password_stored"`
}

// Patch carries the fields the person changed; a field left out stays.
type Patch struct {
	Language    *string           `json:"language,omitempty"`
	Retention   *config.Retention `json:"retention,omitempty"`
	DropPercent *int              `json:"deletion_drop_percent,omitempty"`
	Extra       *[]string         `json:"extra,omitempty"`
	Exclude     *[]string         `json:"exclude,omitempty"`
	Tray        *bool             `json:"tray,omitempty"`
	// Profiles switches what is saved. Saving it also changes the tasks in the
	// scheduler.
	Profiles *config.Profiles `json:"profiles,omitempty"`
}

// envelope is the shape of every answer, the same as the command line's.
type envelope struct {
	OK      bool              `json:"ok"`
	Command string            `json:"command"`
	Data    any               `json:"data,omitempty"`
	Error   *i18n.Description `json:"error,omitempty"`
}

func marshal(value envelope) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"ok":false,"command":"ui","error":{"code":"error.unexpected","message":"encoding failed"}}`)
	}
	return data
}
