//go:build windows

package secrets

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// The file with the new password may be opened by the current user only.
func TestWriteTempIsReadableByTheUserOnly(t *testing.T) {
	path, remove, err := WriteTemp(filepath.Join(t.TempDir(), "tmp"), "SECRET-VALUE")
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	out, err := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe"), path).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	current, _ := user.Current()
	text := string(out)
	// One entry for the file: the user's. Nothing inherited from the folder.
	entries := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, ":(") {
			entries++
			if strings.Contains(line, "(I)") {
				t.Errorf("inherited right left: %s", line)
			}
		}
	}
	if entries != 1 {
		t.Errorf("%d access entries, want 1 (%s):\n%s", entries, current.Username, text)
	}
}
