package restore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func sampleTree(t *testing.T) (string, *Tree) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "source dir")
	if err := GenerateSample(dir); err != nil {
		t.Fatal(err)
	}
	tree, err := HashTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, tree
}

func TestGenerateSampleHasLongPath(t *testing.T) {
	_, tree := sampleTree(t)
	if len(tree.Files) != 5 {
		t.Fatalf("want 5 files, got %d", len(tree.Files))
	}
	longest := 0
	for _, file := range tree.Files {
		longest = max(longest, len([]rune(file.Path)))
	}
	if longest <= 260 {
		t.Errorf("longest path is only %d characters", longest)
	}
	if tree.Bytes < sampleRandomBytes {
		t.Errorf("tree has %d bytes", tree.Bytes)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompareMatchesCopies(t *testing.T) {
	dir, want := sampleTree(t)
	copied := filepath.Join(t.TempDir(), "copy")
	copyTree(t, dir, copied)
	got, err := HashTree(copied)
	if err != nil {
		t.Fatal(err)
	}
	if want.Digest != got.Digest {
		t.Error("digests differ for identical trees")
	}
	if diff := Compare(want, got); len(diff) != 0 {
		t.Errorf("unexpected differences: %v", diff)
	}
}

func TestCompareFindsEveryKindOfDifference(t *testing.T) {
	dir, want := sampleTree(t)
	copied := filepath.Join(t.TempDir(), "copy")
	copyTree(t, dir, copied)

	if err := os.WriteFile(filepath.Join(copied, "readme.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(copied, "пустой файл.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := HashTree(copied)
	if err != nil {
		t.Fatal(err)
	}
	if want.Digest == got.Digest {
		t.Error("digests equal for different trees")
	}
	reasons := map[string]string{}
	for _, difference := range Compare(want, got) {
		reasons[difference.Path] = difference.Reason
	}
	expected := map[string]string{"readme.txt": ReasonChanged, "пустой файл.txt": ReasonMissing, "new.txt": ReasonExtra}
	if len(reasons) != len(expected) {
		t.Fatalf("got %v", reasons)
	}
	for path, reason := range expected {
		if reasons[path] != reason {
			t.Errorf("%s: got %q, want %q", path, reasons[path], reason)
		}
	}
}

func TestHashTreeMissingFolder(t *testing.T) {
	if _, err := HashTree(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRestoredPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		got := RestoredPath(`C:\target`, `D:\Папка\source`)
		if want := `C:\target\D\Папка\source`; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		return
	}
	if got := RestoredPath("/target", "/home/user/source"); got != "/target/home/user/source" {
		t.Errorf("got %q", got)
	}
}
