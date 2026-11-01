// Package manifests embeds what BackUpClaude knows about Claude's data on each
// OS. Paths of Claude's data live here and nowhere else in the code.
package manifests

import (
	"bytes"
	"embed"
	"encoding/json"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

//go:embed windows.json darwin.json linux.json
var files embed.FS

const schemaVersion = 1

// Manifest lists Claude's data locations for one OS. Paths use forward
// slashes and may start with ~/ or contain %VAR%.
type Manifest struct {
	SchemaVersion int      `json:"schema_version"`
	OS            string   `json:"os"`
	ClaudeHome    string   `json:"claude_home"`
	DesktopRoots  []string `json:"desktop_roots"`
	Include       []string `json:"include"`
	Exclude       []string `json:"exclude"`
}

// Load returns the embedded manifest for a GOOS value.
func Load(goos string) (*Manifest, error) {
	data, err := files.ReadFile(goos + ".json")
	if err != nil {
		return nil, i18n.E("manifest.unsupported_os", goos)
	}
	var manifest Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return nil, i18n.E("manifest.invalid", goos).Because(err)
	}
	if manifest.SchemaVersion != schemaVersion || manifest.OS != goos || manifest.ClaudeHome == "" {
		return nil, i18n.E("manifest.invalid", goos)
	}
	return &manifest, nil
}
