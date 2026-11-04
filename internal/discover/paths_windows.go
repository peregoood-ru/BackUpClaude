//go:build windows

package discover

import (
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const longPathBuffer = 32768

// gitBashPath is the form Git Bash gives a Windows path: /d/work/project.
var gitBashPath = regexp.MustCompile(`^/([a-zA-Z])(/.*)?$`)

// normalizePath brings a path from a transcript to one spelling. It returns
// false for a path that does not belong to this OS, such as a folder inside a
// Linux sandbox.
func normalizePath(raw string) (string, bool) {
	path := strings.TrimSpace(raw)
	path = strings.TrimPrefix(path, `\\?\`)
	if match := gitBashPath.FindStringSubmatch(path); match != nil {
		path = match[1] + ":" + match[2] + "/"
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		return "", false
	}
	path = filepath.Clean(path)
	if len(path) >= 2 && path[1] == ':' {
		path = strings.ToUpper(path[:1]) + path[1:]
	}
	return longName(path), true
}

// pathKey makes paths comparable: Windows ignores case in file names.
func pathKey(path string) string { return strings.ToLower(path) }

// longName replaces 8.3 short names such as USER~1. Temp folders are often
// spelled that way and may be gone by now, so what cannot be resolved stays and
// the longest existing beginning of the path is resolved instead.
func longName(path string) string {
	if !strings.Contains(path, "~") {
		return path
	}
	rest := ""
	for current := path; ; {
		if resolved, ok := getLongPathName(current); ok {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
}

func getLongPathName(path string) (string, bool) {
	wide, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	buffer := make([]uint16, longPathBuffer)
	length, err := syscall.GetLongPathName(wide, &buffer[0], longPathBuffer)
	if err != nil || length == 0 || length >= longPathBuffer {
		return "", false
	}
	return syscall.UTF16ToString(buffer[:length]), true
}
