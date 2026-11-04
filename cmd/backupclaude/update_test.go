package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/selfinstall"
	"github.com/peregoood-ru/BackUpClaude/internal/update"
)

// makeArchive packs a program the way the release does for this system.
func makeArchive(t *testing.T, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if runtime.GOOS == "windows" {
		w := zip.NewWriter(&buffer)
		for name, body := range map[string]string{"LICENSE": "MIT", selfinstall.Name(): content} {
			f, _ := w.Create(name)
			_, _ = f.Write([]byte(body))
		}
		_ = w.Close()
		return buffer.Bytes()
	}
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: selfinstall.Name(), Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	_ = gz.Close()
	return buffer.Bytes()
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type releaseServer struct {
	archive, sums []byte
	archiveName   string
	token         string
	hits          int
}

// newReleaseServer publishes one release with the given program.
func newReleaseServer(t *testing.T, w *world, tag, content string, mutate func(*releaseServer)) *releaseServer {
	t.Helper()
	s := &releaseServer{archive: makeArchive(t, content)}
	s.archiveName = update.ArchiveName(tag, runtime.GOOS, runtime.GOARCH)
	s.sums = []byte(sha(s.archive) + "  " + s.archiveName + "\n")
	if mutate != nil {
		mutate(s)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases", func(rw http.ResponseWriter, r *http.Request) {
		s.hits++
		if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			http.NotFound(rw, r)
			return
		}
		assets := []update.Asset{{ID: 1, Name: s.archiveName}, {ID: 2, Name: update.ChecksumsFile}}
		_ = json.NewEncoder(rw).Encode([]update.Release{{Tag: "v" + tag, Assets: assets}})
	})
	mux.HandleFunc("/repos/o/r/releases/assets/1", func(rw http.ResponseWriter, _ *http.Request) { _, _ = rw.Write(s.archive) })
	mux.HandleFunc("/repos/o/r/releases/assets/2", func(rw http.ResponseWriter, _ *http.Request) { _, _ = rw.Write(s.sums) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	w.releases = func(token string) *update.Client {
		return &update.Client{Repo: "o/r", API: srv.URL, Token: token, HTTP: srv.Client()}
	}
	return s
}

func asVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// programRunner answers --version and tools install like a new program would.
func programRunner(version string, fail bool) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, program string, args ...string) ([]byte, error) {
		if fail {
			return nil, errors.New("does not start")
		}
		if len(args) > 0 && args[0] == "--version" {
			return []byte(fmt.Sprintf(`{"ok":true,"command":"version","data":{"version":%q}}`, version)), nil
		}
		return []byte(`{"ok":true}`), nil
	}
}

func newUpdateWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.exe = filepath.Join(t.TempDir(), selfinstall.Name())
	fakehome.Write(t, w.exe, "program v1")
	return w
}

func TestUpdateOfAPrivateRepositoryExplainsItself(t *testing.T) {
	asVersion(t, "0.1.0")
	w := newUpdateWorld(t)
	newReleaseServer(t, w, "0.2.0", "program v2", func(s *releaseServer) { s.token = "needed" })
	t.Setenv(update.EnvToken, "")
	r := w.run("update", "--json")
	parsed := r.envelope(t)
	if r.code != 1 || parsed.OK || parsed.Error.Code != "update.no_access" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(parsed.Error.Hint, update.EnvToken) || !strings.Contains(parsed.Error.Message, "private") {
		t.Errorf("the error does not explain what to do: %+v", parsed.Error)
	}
	if data, _ := os.ReadFile(w.exe); string(data) != "program v1" {
		t.Error("the program changed")
	}
	// With the token it works, and the token never appears in the answer.
	t.Setenv(update.EnvToken, "needed")
	w.runProgram = programRunner("0.2.0", false)
	r = w.run("update", "--json")
	if r.code != 0 || strings.Contains(r.stdout, "needed") || strings.Contains(r.stderr, "needed") {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateCheckOnly(t *testing.T) {
	asVersion(t, "0.1.0")
	w := newUpdateWorld(t)
	newReleaseServer(t, w, "0.2.0", "program v2", nil)
	r := w.run("update", "--check", "--json")
	var report updateReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil || r.code != 0 {
		t.Fatalf("%v %+v", err, r)
	}
	if !report.UpdateAvailable || report.Updated || report.Latest != "0.2.0" || report.Current != "0.1.0" {
		t.Errorf("%+v", report)
	}
	if data, _ := os.ReadFile(w.exe); string(data) != "program v1" {
		t.Error("--check changed the program")
	}
	asVersion(t, "0.2.0")
	r = w.run("update", "--json")
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil || report.UpdateAvailable || report.Updated {
		t.Errorf("%+v %v", report, err)
	}
}

func TestUpdateReplacesTheProgramAndTouchesNothingElse(t *testing.T) {
	asVersion(t, "0.1.0")
	w := newUpdateWorld(t)
	newReleaseServer(t, w, "0.2.0", "program v2", nil)

	w.runProgram = programRunner("0.2.0", false)
	// A working installation: settings and copies must not change.
	saveConfig(t, nil)
	layoutFile := filepath.Join(os.Getenv("BACKUPCLAUDE_HOME"), "config.json")
	before := fileHash(t, layoutFile)

	r := w.run("update", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var report updateReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Updated || !report.Verified || report.Latest != "0.2.0" || report.ToolsOK == nil || !*report.ToolsOK {
		t.Errorf("%+v", report)
	}
	if data, _ := os.ReadFile(w.exe); string(data) != "program v2" {
		t.Errorf("the program was not replaced: %q", data)
	}
	if fileHash(t, layoutFile) != before {
		t.Error("config.json changed")
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(w.exe), ".backupclaude-new-*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left: %v", leftovers)
	}
}

func TestUpdateRefusesAWrongChecksum(t *testing.T) {
	asVersion(t, "0.1.0")
	w := newUpdateWorld(t)
	newReleaseServer(t, w, "0.2.0", "program v2", func(s *releaseServer) {
		s.sums = []byte(strings.Repeat("0", 64) + "  " + s.archiveName + "\n")
	})
	w.runProgram = programRunner("0.2.0", false)
	r := w.run("update", "--json")
	if r.code == 0 || r.envelope(t).Error.Code != "update.hash_mismatch" {
		t.Fatalf("%+v", r)
	}
	if data, _ := os.ReadFile(w.exe); string(data) != "program v1" {
		t.Error("a file with a wrong checksum was installed")
	}
}

func TestUpdateRefusesAReleaseWithoutChecksum(t *testing.T) {
	asVersion(t, "0.1.0")
	w := newUpdateWorld(t)
	newReleaseServer(t, w, "0.2.0", "program v2", func(s *releaseServer) { s.sums = []byte("nothing useful\n") })
	w.runProgram = programRunner("0.2.0", false)
	if r := w.run("update", "--json"); r.code == 0 || r.envelope(t).Error.Code != "update.no_checksum" {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateKeepsTheOldProgramWhenTheNewOneDoesNotStart(t *testing.T) {
	asVersion(t, "0.1.0")
	w := newUpdateWorld(t)
	newReleaseServer(t, w, "0.2.0", "program v2", nil)
	w.runProgram = programRunner("", true)
	if r := w.run("update", "--json"); r.code == 0 || r.envelope(t).Error.Code != "update.smoke_failed" {
		t.Fatalf("%+v", r)
	}
	if data, _ := os.ReadFile(w.exe); string(data) != "program v1" {
		t.Error("the old program was replaced by one that does not start")
	}
	w.runProgram = programRunner("0.3.0", false)
	if r := w.run("update", "--json"); r.code == 0 || r.envelope(t).Error.Code != "update.wrong_version" {
		t.Fatalf("a program that reports another version: %+v", r)
	}
}

func TestUpdateOfADevelopmentBuild(t *testing.T) {
	asVersion(t, "dev")
	w := newUpdateWorld(t)
	newReleaseServer(t, w, "0.2.0", "program v2", nil)
	w.runProgram = programRunner("0.2.0", false)
	if r := w.run("update", "--json"); r.code == 0 || r.envelope(t).Error.Code != "update.dev_build" {
		t.Fatalf("%+v", r)
	}
	if r := w.run("update", "--check", "--json"); r.code != 0 {
		t.Errorf("%+v", r)
	}
	if r := w.run("update", "--force", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if data, _ := os.ReadFile(w.exe); string(data) != "program v2" {
		t.Errorf("%q", data)
	}
}

func TestUpdateFromAFile(t *testing.T) {
	asVersion(t, "0.1.0")
	w := newUpdateWorld(t)
	archive := makeArchive(t, "program v3")
	file := filepath.Join(t.TempDir(), "backupclaude_0.3.0.archive"+map[bool]string{true: ".zip", false: ".tar.gz"}[runtime.GOOS == "windows"])
	if err := os.WriteFile(file, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	w.runProgram = programRunner("0.3.0", false)

	if r := w.run("update", "--from-file", file, "--sha256", strings.Repeat("1", 64), "--json"); r.code == 0 || r.envelope(t).Error.Code != "update.hash_mismatch" {
		t.Fatalf("a wrong hash: %+v", r)
	}
	if data, _ := os.ReadFile(w.exe); string(data) != "program v1" {
		t.Error("installed despite the wrong hash")
	}
	r := w.run("update", "--from-file", file, "--sha256", sha(archive), "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var report updateReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil || !report.Updated || !report.Verified || report.Source != "file" || report.Latest != "0.3.0" {
		t.Errorf("%+v %v", report, err)
	}
	if data, _ := os.ReadFile(w.exe); string(data) != "program v3" {
		t.Errorf("%q", data)
	}
}

func TestUpdateReplacesAProgramThatIsRunning(t *testing.T) {
	// The test binary itself is the running program here: it can be renamed
	// but not overwritten on Windows. The swap must work on a copy that runs.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(self)
	if err != nil || info.Size() > 200<<20 {
		t.Skip("cannot copy the test binary")
	}
	data, _ := os.ReadFile(self)
	copyPath := filepath.Join(t.TempDir(), selfinstall.Name())
	if err := os.WriteFile(copyPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	// Start the copy so that the file is held open by a running process.
	cmd := startHolding(t, copyPath)
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	staged := filepath.Join(filepath.Dir(copyPath), ".backupclaude-new-test")
	if err := os.WriteFile(staged, []byte("new program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := selfinstall.Swap(staged, copyPath); err != nil {
		t.Fatalf("swap with a running program: %v", err)
	}
	if got, _ := os.ReadFile(copyPath); string(got) != "new program" {
		t.Error("the new program is not in place")
	}
}

// startHolding starts a program that does nothing but stay alive, so that its
// file is in use.
func startHolding(t *testing.T, program string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(program)
	cmd.Env = append(os.Environ(), envHold+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	return cmd
}
