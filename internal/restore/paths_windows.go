//go:build windows

package restore

import (
	"path/filepath"
	"regexp"
	"strings"
)

var driveFolder = regexp.MustCompile(`^/([A-Za-z])(/.*)?$`)

func localPath(snapshotPath string) string {
	match := driveFolder.FindStringSubmatch(snapshotPath)
	if match == nil {
		return filepath.FromSlash(snapshotPath)
	}
	rest := match[2]
	if rest == "" {
		rest = "/"
	}
	return strings.ToUpper(match[1]) + ":" + filepath.FromSlash(rest)
}

// fold makes paths comparable: Windows ignores case in file names.
func fold(path string) string { return strings.ToLower(path) }
