// Package selfinstall puts the program file in its place and swaps it for a new
// one. On Windows a running .exe cannot be overwritten or deleted, but it can
// be renamed, so both steps move the old file aside first and clean it up at
// the next start.
package selfinstall

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Name is the file name of the program.
func Name() string {
	if runtime.GOOS == "windows" {
		return "backupclaude.exe"
	}
	return "backupclaude"
}

// oldSuffix marks the file that was in place before a swap.
const oldSuffix = ".old"

// Target is where the program lives for the user: inside dir.
func Target(dir string) string { return filepath.Join(dir, Name()) }

// SameFile reports whether two paths lead to one file.
func SameFile(a, b string) bool {
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(first, second)
}

// Place copies the file at source to target. A copy that is already there and
// is the source itself is left alone. The old file at target, running or not,
// is renamed to target.old and removed when it can be.
func Place(source, target string) (placed bool, err error) {
	if SameFile(source, target) {
		return false, nil
	}
	staged, err := stage(source, target)
	if err != nil {
		return false, err
	}
	if err := Swap(staged, target); err != nil {
		_ = os.Remove(staged)
		return false, err
	}
	return true, nil
}

// stage copies source next to target under a temporary name, so that the final
// step is one rename inside one folder.
func stage(source, target string) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", i18n.E("selfinstall.read_failed", source).Because(err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", i18n.E("selfinstall.write_failed", filepath.Dir(target)).Because(err)
	}
	out, err := os.CreateTemp(filepath.Dir(target), ".backupclaude-new-*")
	if err != nil {
		return "", i18n.E("selfinstall.write_failed", filepath.Dir(target)).Because(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(out.Name())
		return "", i18n.E("selfinstall.write_failed", target).Because(err)
	}
	if err := os.Chmod(out.Name(), 0o755); err != nil {
		_ = os.Remove(out.Name())
		return "", i18n.E("selfinstall.write_failed", target).Because(err)
	}
	return out.Name(), nil
}

// moveAside renames the file at path out of the way and returns the new name.
// The usual name is path.old; if that cannot be freed, because a scanner or an
// earlier leftover holds it, a name with a number is used.
func moveAside(path string) (string, error) {
	var last error
	for i := 0; i < 20; i++ {
		name := path + oldSuffix
		if i > 0 {
			name = fmt.Sprintf("%s%s-%d", path, oldSuffix, i)
		}
		_ = os.Remove(name)
		err := os.Rename(path, name)
		if err == nil {
			return name, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		last = err
		if i == 2 {
			time.Sleep(300 * time.Millisecond)
		}
	}
	return "", last
}

// Swap moves the file staged next to target into place. If target exists it is
// renamed aside first (a running program can be renamed, not overwritten); if
// the second rename fails the old file comes back. The staged file must sit in
// the same folder as target.
func Swap(staged, target string) error {
	old, err := moveAside(target)
	hadOld := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return i18n.E("selfinstall.swap_failed", target).Hint("selfinstall.swap_failed.hint").Because(err)
	}
	if err := os.Rename(staged, target); err != nil {
		if hadOld {
			_ = os.Rename(old, target)
		}
		return i18n.E("selfinstall.swap_failed", target).Hint("selfinstall.swap_failed.hint").Because(err)
	}
	// Works when the old file was not running; otherwise CleanOld gets it later.
	_ = os.Remove(old)
	return nil
}

// Stage copies an already unpacked file next to target, for Swap.
func Stage(source, target string) (string, error) { return stage(source, target) }

// CleanOld removes the files a swap left behind. It never reports a failure: a
// file that is still running is simply tried again at the next start.
func CleanOld(target string) {
	leftovers, _ := filepath.Glob(escapeGlob(target) + oldSuffix + "*")
	for _, name := range leftovers {
		_ = os.Remove(name)
	}
}

// escapeGlob protects the characters of a path that a pattern would read.
func escapeGlob(path string) string {
	return strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]").Replace(path)
}

// Remove deletes the program file at target. A running program cannot delete
// itself on Windows: the file is renamed and the new name is returned, so that
// the caller can arrange its removal once the program has ended. If even the
// rename is refused, the file itself is returned for the same purpose.
func Remove(target string) (leftover string, err error) {
	err = os.Remove(target)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if moved, moveErr := moveAside(target); moveErr == nil {
		return moved, nil
	}
	return target, nil
}
