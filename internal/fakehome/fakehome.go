// Package fakehome gives tests a private copy of testdata/fake-home: a home
// folder with a made-up ~/.claude and both locations of the desktop application's
// data. Tests never touch the real ones.
package fakehome

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Install says which desktop application data the copy keeps.
type Install int

// Installations.
const (
	Both Install = iota
	Classic
	Store
	NoDesktop
)

// Fixture parts.
const (
	ClassicRoot = "AppData/Roaming/Claude"
	StoreRoot   = "AppData/Local/Packages/Claude_fixture/LocalCache/Roaming/Claude"
	// Repository is a project that is a git repository, with Cyrillic and spaces
	// in its path.
	Repository = "Мои проекты/Сайт клуба"
	// PlainFolder is a project without git.
	PlainFolder = "Мои проекты/Без git"
)

func source() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot locate the fixture")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fake-home")
}

// Copy makes the private home folder. Git refuses to store a .git folder inside
// a repository, so the one in the fixture is created here.
func Copy(t testing.TB, install Install) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "дом пользователя")
	if err := os.CopyFS(home, os.DirFS(source())); err != nil {
		t.Fatal(err)
	}
	Write(t, filepath.Join(home, filepath.FromSlash(Repository), ".git", "HEAD"), "ref: refs/heads/main\n")
	if err := os.MkdirAll(filepath.Join(home, "AppData", "Local", "Temp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if install == Store || install == NoDesktop {
		Remove(t, filepath.Join(home, filepath.FromSlash(ClassicRoot)))
	}
	if install == Classic || install == NoDesktop {
		Remove(t, filepath.Join(home, "AppData", "Local", "Packages"))
	}
	return home
}

// Lookup answers for environment variables of the fake home.
func Lookup(home string) func(string) string {
	values := map[string]string{
		"APPDATA":      filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA": filepath.Join(home, "AppData", "Local"),
		"TEMP":         filepath.Join(home, "AppData", "Local", "Temp"),
		"TMP":          filepath.Join(home, "AppData", "Local", "Temp"),
	}
	return func(name string) string { return values[name] }
}

// Write creates a file with its folders.
func Write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Remove deletes a tree.
func Remove(t testing.TB, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}

// Line is one transcript line that names a working folder.
func Line(cwd string) string {
	value, err := json.Marshal(cwd)
	if err != nil {
		panic(err)
	}
	return `{"type":"user","parentUuid":null,"cwd":` + string(value) + `,"message":{"role":"user","content":"hello"}}`
}

// Transcript writes a session with one line per working folder.
func Transcript(t testing.TB, home, folder, id string, cwds ...string) string {
	t.Helper()
	lines := make([]string, len(cwds))
	for i, cwd := range cwds {
		lines[i] = Line(cwd)
	}
	path := filepath.Join(home, ".claude", "projects", folder, id+".jsonl")
	Write(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// Tree lists the files under root with forward slashes.
func Tree(t testing.TB, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
