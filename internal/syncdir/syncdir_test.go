package syncdir

import (
	"path/filepath"
	"testing"
)

func probe(env map[string]string, existing ...string) Probe {
	return Probe{
		Lookup: func(name string) string { return env[name] },
		Exists: func(path string) bool {
			for _, item := range existing {
				if filepath.Clean(item) == filepath.Clean(path) {
					return true
				}
			}
			return false
		},
	}
}

func volumeRoot(t *testing.T) string {
	t.Helper()
	return filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
}

func TestDetectByName(t *testing.T) {
	root := volumeRoot(t)
	join := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	cases := []struct {
		path    string
		service string
	}{
		{join("YandexDisk", "проекты", "repo"), YandexDisk},
		{join("Users", "Ivan", "Yandex.Disk", "repo"), YandexDisk},
		{join("Users", "Ivan", "Yandex Disk", "x"), YandexDisk},
		{join("Users", "Ivan", "Яндекс.Диск", "x"), YandexDisk},
		{join("Users", "Ivan", "OneDrive", "Backup"), OneDrive},
		{join("Users", "Ivan", "OneDrive - Contoso", "Backup"), OneDrive},
		{join("Users", "Ivan", "Dropbox", "repo"), Dropbox},
		{join("Users", "Ivan", "Dropbox (Personal)", "repo"), Dropbox},
		{join("Users", "Ivan", "Google Drive", "repo"), GoogleDrive},
		{join("Мой диск", "repo"), GoogleDrive},
		{join("YANDEXDISK"), YandexDisk},
	}
	for _, c := range cases {
		got, ok := Detect(c.path, probe(nil))
		if !ok || got.Service != c.service {
			t.Errorf("%s: got %+v %v, want %s", c.path, got, ok, c.service)
		}
	}
}

func TestDetectLeavesOrdinaryFoldersAlone(t *testing.T) {
	root := volumeRoot(t)
	for _, path := range []string{
		filepath.Join(root, "ClaudeBackup", "repo"),
		filepath.Join(root, "Users", "Ivan", "ClaudeBackup"),
		filepath.Join(root, "Users", "Ivan", "Мой диск", "x"), // not at the top of a drive
		filepath.Join(root, "backups", "cloud"),
		root,
	} {
		if got, ok := Detect(path, probe(nil)); ok {
			t.Errorf("%s taken for %+v", path, got)
		}
	}
}

func TestDetectByEnvironment(t *testing.T) {
	root := volumeRoot(t)
	sync := filepath.Join(root, "Users", "Ivan", "Cloud")
	got, ok := Detect(filepath.Join(sync, "repo"), probe(map[string]string{"OneDriveCommercial": sync}))
	if !ok || got.Service != OneDrive || got.Root != sync {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := Detect(filepath.Join(sync+"2", "repo"), probe(map[string]string{"OneDriveCommercial": sync})); ok {
		t.Error("a sibling with the same prefix was taken")
	}
}

func TestDetectByMarkerAndRegistryRoot(t *testing.T) {
	root := volumeRoot(t)
	box := filepath.Join(root, "Data", "Work")
	got, ok := Detect(filepath.Join(box, "deep", "repo"), probe(nil, filepath.Join(box, ".dropbox")))
	if !ok || got.Service != Dropbox || got.Root != box {
		t.Fatalf("marker: %+v %v", got, ok)
	}
	p := probe(nil)
	p.Roots = []Match{{Service: YandexDisk, Root: filepath.Join(root, "Files")}, {Root: filepath.Join(root, "Other")}}
	if got, ok := Detect(filepath.Join(root, "files", "repo"), p); !ok || got.Service != YandexDisk {
		t.Errorf("registry root: %+v %v", got, ok)
	}
	if got, ok := Detect(filepath.Join(root, "Other"), p); !ok || got.Service != Other {
		t.Errorf("unnamed root: %+v %v", got, ok)
	}
}

func TestDetectYandexDiskInAFolderWithAnotherName(t *testing.T) {
	root := volumeRoot(t)
	box := filepath.Join(root, "Data", "Sync")
	got, ok := Detect(filepath.Join(box, "deep", "repo"), probe(nil, filepath.Join(box, ".sync", "~special")))
	if !ok || got.Service != YandexDisk || got.Root != box {
		t.Fatalf("%+v %v", got, ok)
	}
}

func TestSystemProbeRuns(t *testing.T) {
	// Only checks that reading the real registry or nothing does not crash.
	_ = SystemProbe()
}
