// Package pathenv adds the application's folder to the PATH of the user, so
// that `backupclaude` works in any terminal, and takes it out again. It never
// needs administrator rights: on Windows only the user's own variables change.
package pathenv

import (
	"path"
	"strings"
)

// Separator joins the entries of PATH on Windows. Other systems keep PATH in
// shell profiles, which stage 9 handles.
const separator = ";"

// Add returns the PATH value that holds dir, and whether it had to change.
func add(current, dir string, expand func(string) string) (string, bool) {
	for _, entry := range split(current) {
		if same(expand(entry), dir) {
			return current, false
		}
	}
	if strings.TrimSpace(current) == "" {
		return dir, true
	}
	return strings.TrimRight(current, separator) + separator + dir, true
}

// remove returns the PATH value without dir, and whether it changed.
func remove(current, dir string, expand func(string) string) (string, bool) {
	var kept []string
	changed := false
	for _, entry := range split(current) {
		if same(expand(entry), dir) {
			changed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !changed {
		return current, false
	}
	return strings.Join(kept, separator), true
}

func contains(current, dir string, expand func(string) string) bool {
	for _, entry := range split(current) {
		if same(expand(entry), dir) {
			return true
		}
	}
	return false
}

func split(value string) []string {
	var entries []string
	for _, entry := range strings.Split(value, separator) {
		if strings.TrimSpace(entry) != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// same compares two folders the way Windows does: case does not matter, both
// kinds of slash count, and a trailing separator does not. It works the same on
// every system, since a Windows PATH is read by tests everywhere.
func same(a, b string) bool {
	clean := func(value string) string {
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if value == "" {
			return ""
		}
		return strings.ToLower(path.Clean(strings.ReplaceAll(value, `\`, "/")))
	}
	return clean(a) != "" && clean(a) == clean(b)
}
