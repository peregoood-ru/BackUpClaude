// Package engine installs restic and rclone and talks to them. BackUpClaude
// runs both as separate programs and never links their code.
package engine

import (
	"bytes"
	"encoding/json"
	"regexp"
	"runtime"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Names of the external tools in tools.lock.json.
const (
	ToolRestic = "restic"
	ToolRclone = "rclone"
)

const lockSchemaVersion = 1

// Lock is the content of tools.lock.json.
type Lock struct {
	SchemaVersion int             `json:"schema_version"`
	Tools         map[string]Tool `json:"tools"`
}

// Tool pins one program to a version and to its release files.
type Tool struct {
	Version    string           `json:"version"`
	Repository string           `json:"repository"`
	Tag        string           `json:"tag"`
	Assets     map[string]Asset `json:"assets"`
}

// Asset is one downloadable release file for a platform.
type Asset struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	// Entry is the path of the program inside a zip archive.
	Entry string `json:"entry,omitempty"`
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ParseLock decodes and checks tools.lock.json.
func ParseLock(data []byte) (*Lock, error) {
	var lock Lock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lock); err != nil {
		return nil, i18n.E("engine.lock.invalid").Because(err)
	}
	if lock.SchemaVersion != lockSchemaVersion {
		return nil, i18n.E("engine.lock.schema", lock.SchemaVersion)
	}
	for name, tool := range lock.Tools {
		if tool.Version == "" || tool.Repository == "" || tool.Tag == "" || len(tool.Assets) == 0 {
			return nil, i18n.E("engine.lock.incomplete", name)
		}
		for platform, asset := range tool.Assets {
			if asset.File == "" || !sha256Pattern.MatchString(asset.SHA256) {
				return nil, i18n.E("engine.lock.bad_asset", name, platform)
			}
		}
	}
	return &lock, nil
}

// Asset returns what to download for a tool on a platform such as
// "windows/amd64".
func (l *Lock) Asset(name, platform string) (Tool, Asset, error) {
	tool, ok := l.Tools[name]
	if !ok {
		return Tool{}, Asset{}, i18n.E("engine.lock.unknown_tool", name)
	}
	asset, ok := tool.Assets[platform]
	if !ok {
		return Tool{}, Asset{}, i18n.E("engine.lock.no_platform", name, platform)
	}
	return tool, asset, nil
}

// Platform returns the current platform in the form used by the lock file.
func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
