//go:build !windows

package restore

import "path/filepath"

func localPath(snapshotPath string) string { return filepath.FromSlash(snapshotPath) }

// fold makes paths comparable; these file systems tell case apart.
func fold(path string) string { return path }
