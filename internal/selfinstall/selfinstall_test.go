package selfinstall

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPlaceCopiesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "download", Name())
	target := Target(filepath.Join(dir, "data", "bin"))
	write(t, source, "version one")

	placed, err := Place(source, target)
	if err != nil || !placed || read(t, target) != "version one" {
		t.Fatalf("first place: %v %v", placed, err)
	}
	write(t, source, "version two")
	if placed, err := Place(source, target); err != nil || !placed || read(t, target) != "version two" {
		t.Fatalf("replace: %v %v", placed, err)
	}
	if _, err := os.Stat(target + oldSuffix); err == nil {
		t.Error("the old file was not cleaned up")
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Errorf("leftovers in the folder: %v", entries)
	}
}

func TestPlaceDoesNothingForTheSameFile(t *testing.T) {
	target := Target(t.TempDir())
	write(t, target, "same")
	if placed, err := Place(target, target); err != nil || placed {
		t.Fatalf("%v %v", placed, err)
	}
	if read(t, target) != "same" {
		t.Error("file changed")
	}
}

func TestSwapKeepsTheOldFileWhenThereIsNothingToSwapIn(t *testing.T) {
	target := Target(t.TempDir())
	write(t, target, "old")
	if err := Swap(filepath.Join(filepath.Dir(target), "missing"), target); err == nil {
		t.Fatal("swap with a missing file succeeded")
	}
	if read(t, target) != "old" {
		t.Error("the old file did not come back")
	}
}

func TestRemoveAndCleanOld(t *testing.T) {
	target := Target(t.TempDir())
	write(t, target, "x")
	leftover, err := Remove(target)
	if err != nil || leftover != "" {
		t.Fatalf("%q %v", leftover, err)
	}
	if leftover, err := Remove(target); err != nil || leftover != "" {
		t.Errorf("removing a missing file: %q %v", leftover, err)
	}
	write(t, target+oldSuffix, "left")
	CleanOld(target)
	if _, err := os.Stat(target + oldSuffix); err == nil {
		t.Error("CleanOld left the file")
	}
}

func TestDeleteLaterIsHarmlessForMissingFiles(t *testing.T) {
	if err := DeleteLater([]string{filepath.Join(t.TempDir(), "no-such-file")}, nil); err != nil {
		t.Fatal(err)
	}
}
