package sysproc

import (
	"path/filepath"
	"strings"
)

// Process is a running program as the OS lists it.
type Process struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	// Path is the full path of the executable; it is empty when the OS does
	// not tell it for this process.
	Path string `json:"path,omitempty"`
}

// Find returns the running processes with one of the names, compared without
// regard to case. When markers are given, the path of the executable must also
// contain one of them (with forward slashes, any case). A process whose path
// cannot be read counts as a match: the answer is used to keep files of a
// running program alone, and a wrong "running" only asks the user to close it.
func Find(names, markers []string) ([]Process, error) {
	all, err := list()
	if err != nil {
		return nil, err
	}
	var found []Process
	for _, process := range all {
		if Matches(process, names, markers) {
			found = append(found, process)
		}
	}
	return found, nil
}

// Matches applies the rules of Find to one process.
func Matches(process Process, names, markers []string) bool {
	nameMatch := false
	for _, name := range names {
		if strings.EqualFold(process.Name, name) || strings.EqualFold(filepath.Base(filepath.ToSlash(process.Path)), name) {
			nameMatch = true
			break
		}
	}
	if !nameMatch {
		return false
	}
	if len(markers) == 0 || process.Path == "" {
		return true
	}
	path := strings.ToLower(filepath.ToSlash(strings.ReplaceAll(process.Path, `\`, "/")))
	for _, marker := range markers {
		if strings.Contains(path, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}
