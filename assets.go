// Package backupclaude holds the files embedded from the repository root.
package backupclaude

import _ "embed"

// ToolsLock is the content of tools.lock.json: pinned versions and SHA-256 of
// the external programs.
//
//go:embed tools.lock.json
var ToolsLock []byte
