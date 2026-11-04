// Package syncdir tells whether a folder belongs to a file synchronisation
// service. The local copy must not live there: a sync service copies a deleted
// or damaged file to the cloud, and the files of a repository are written and
// rewritten all the time. The check knows Yandex Disk, OneDrive, Google Drive
// and Dropbox.
package syncdir

import (
	"os"
	"path/filepath"
	"strings"
)

// Services the check recognises.
const (
	YandexDisk  = "yandex_disk"
	OneDrive    = "onedrive"
	GoogleDrive = "google_drive"
	Dropbox     = "dropbox"
	// Other is a folder the OS itself reports as a synchronised root.
	Other = "sync_root"
)

// Match says which service owns a folder and where its root is.
type Match struct {
	Service string `json:"service"`
	// Root is the synchronised folder that holds the path; it is empty when the
	// service was recognised only by a name in the path.
	Root string `json:"root,omitempty"`
}

// Probe is what the check needs from the computer; tests replace it.
type Probe struct {
	// Lookup reads an environment variable.
	Lookup func(name string) string
	// Exists reports whether a file or folder exists.
	Exists func(path string) bool
	// Roots are folders the OS reports as synchronised (on Windows, the cloud
	// files registry), each with the service name, or Other.
	Roots []Match
}

// SystemProbe is the probe of this computer.
func SystemProbe() Probe {
	return Probe{
		Lookup: os.Getenv,
		Exists: func(path string) bool { _, err := os.Stat(path); return err == nil },
		Roots:  systemRoots(),
	}
}

// markers are files that sit in the root of a synchronised folder.
var markers = []struct {
	name    string
	service string
}{
	{".dropbox", Dropbox},
	{".dropbox.cache", Dropbox},
	{".tmp.driveupload", GoogleDrive},
	{".tmp.drivedownload", GoogleDrive},
	// The service keeps its state in a hidden .sync folder at the root; seen in a
	// real installation on 2026-11-03.
	{filepath.Join(".sync", "~special"), YandexDisk},
	{".shortcut-targets-by-id", GoogleDrive},
}

// Detect returns the service whose folder holds path. The path does not have to
// exist yet: its parents are checked.
func Detect(path string, probe Probe) (Match, bool) {
	if probe.Lookup == nil {
		probe.Lookup = os.Getenv
	}
	if probe.Exists == nil {
		probe.Exists = func(string) bool { return false }
	}
	path = filepath.Clean(path)

	for _, name := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		if root := probe.Lookup(name); root != "" && within(path, root) {
			return Match{Service: OneDrive, Root: root}, true
		}
	}
	for _, root := range probe.Roots {
		if within(path, root.Root) {
			service := root.Service
			if service == "" {
				service = Other
			}
			return Match{Service: service, Root: root.Root}, true
		}
	}
	for dir := path; ; {
		if service := byName(dir); service != "" {
			return Match{Service: service}, true
		}
		for _, marker := range markers {
			if probe.Exists(filepath.Join(dir, marker.name)) {
				return Match{Service: marker.service, Root: dir}, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Match{}, false
		}
		dir = parent
	}
}

// within reports whether path is root or lies inside it.
func within(path, root string) bool {
	root = filepath.Clean(root)
	if root == "." || root == "" {
		return false
	}
	if len(path) < len(root) || !strings.EqualFold(path[:len(root)], root) {
		return false
	}
	return len(path) == len(root) || os.IsPathSeparator(path[len(root)]) || strings.HasSuffix(root, string(filepath.Separator))
}

// byName recognises the usual names of synchronised folders. Spaces, dots,
// hyphens and underscores are ignored: "Yandex.Disk", "Yandex Disk" and
// "YandexDisk" are one name.
func byName(dir string) string {
	base := filepath.Base(dir)
	if base == "." || base == string(filepath.Separator) || strings.HasSuffix(base, ":") {
		return ""
	}
	name := squash(base)
	switch {
	case name == "yandexdisk" || name == "яндексдиск":
		return YandexDisk
	case strings.HasPrefix(name, "onedrive"):
		return OneDrive
	case name == "googledrive" || name == "mygoogledrive" || name == "googleдиск":
		return GoogleDrive
	case name == "dropbox" || strings.HasPrefix(name, "dropbox("):
		return Dropbox
	case name == "мойдиск" && isDriveRoot(filepath.Dir(dir)):
		// Google Drive for desktop mounts "My Drive" at the top of its own drive.
		return GoogleDrive
	}
	return ""
}

func isDriveRoot(path string) bool {
	return filepath.Dir(path) == path
}

func squash(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch r {
		case ' ', '.', '-', '_':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
