package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

const testProgram = "pretend this is a program\n"

func sumOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func zipWith(t *testing.T, name, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range []string{"README.txt", name} {
		file, err := writer.Create(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// fixture serves one release file and counts downloads.
type fixture struct {
	installer *Installer
	downloads *int
}

func newFixture(t *testing.T, platform, file string, body []byte, wantSum string) fixture {
	t.Helper()
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/owner/tool/releases/download/v1.2.3/"+file {
			http.NotFound(w, r)
			return
		}
		count++
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	lock := &Lock{SchemaVersion: 1, Tools: map[string]Tool{
		ToolRestic: {
			Version: "1.2.3", Repository: "owner/tool", Tag: "v1.2.3",
			Assets: map[string]Asset{platform: {File: file, SHA256: wantSum, Entry: "folder/program"}},
		},
	}}
	return fixture{
		installer: &Installer{
			Lock: lock, Dir: filepath.Join(t.TempDir(), "папка с пробелами", "bin"),
			Platform: platform, BaseURL: server.URL,
		},
		downloads: &count,
	}
}

func errorCode(err error) string {
	return i18n.New("en").Describe(err).Code
}

func TestInstallZipChecksHashAndRecordsState(t *testing.T) {
	archive := zipWith(t, "folder/program", testProgram)
	f := newFixture(t, "windows/amd64", "tool.zip", archive, sumOf(archive))

	installed, err := f.installer.Install(context.Background(), ToolRestic)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(installed.Path) != "restic.exe" {
		t.Errorf("path = %s", installed.Path)
	}
	data, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != testProgram {
		t.Errorf("wrong file extracted: %q", data)
	}

	again, err := f.installer.Install(context.Background(), ToolRestic)
	if err != nil {
		t.Fatal(err)
	}
	if *f.downloads != 1 || again.SHA256 != installed.SHA256 {
		t.Errorf("verified copy was downloaded again, %d downloads", *f.downloads)
	}
	entries, err := os.ReadDir(f.installer.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install-") {
			t.Errorf("work folder left behind: %s", entry.Name())
		}
	}
}

func TestInstallBzip2(t *testing.T) {
	archive, err := os.ReadFile(filepath.Join("testdata", "program.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, "linux/amd64", "tool.bz2", archive, sumOf(archive))
	installed, err := f.installer.Install(context.Background(), ToolRestic)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != testProgram {
		t.Errorf("wrong content: %q", data)
	}
	if filepath.Base(installed.Path) != "restic" {
		t.Errorf("path = %s", installed.Path)
	}
}

func TestInstallRefusesWrongHash(t *testing.T) {
	archive := zipWith(t, "folder/program", testProgram)
	f := newFixture(t, "windows/amd64", "tool.zip", archive, strings.Repeat("0", 64))
	_, err := f.installer.Install(context.Background(), ToolRestic)
	if code := errorCode(err); code != "engine.download.hash_mismatch" {
		t.Fatalf("code = %s (%v)", code, err)
	}
	if _, err := f.installer.Locate(ToolRestic); !IsNotInstalled(err) {
		t.Fatalf("Locate after failure: %v", err)
	}
	folders, err := filepath.Glob(filepath.Join(f.installer.Dir, "restic-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 0 {
		t.Errorf("rejected file was installed: %v", folders)
	}
}

func TestInstallEntryMissing(t *testing.T) {
	archive := zipWith(t, "other/program", testProgram)
	f := newFixture(t, "windows/amd64", "tool.zip", archive, sumOf(archive))
	_, err := f.installer.Install(context.Background(), ToolRestic)
	if code := errorCode(err); code != "engine.extract.no_entry" {
		t.Fatalf("code = %s (%v)", code, err)
	}
}

func TestInstallNotFound(t *testing.T) {
	f := newFixture(t, "windows/amd64", "tool.zip", nil, strings.Repeat("0", 64))
	f.installer.BaseURL += "/missing"
	_, err := f.installer.Install(context.Background(), ToolRestic)
	if code := errorCode(err); code != "engine.download.status" {
		t.Fatalf("code = %s (%v)", code, err)
	}
}

func TestLocateNoticesChangedProgram(t *testing.T) {
	archive := zipWith(t, "folder/program", testProgram)
	f := newFixture(t, "windows/amd64", "tool.zip", archive, sumOf(archive))
	installed, err := f.installer.Install(context.Background(), ToolRestic)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed.Path, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = f.installer.Locate(ToolRestic)
	if err == nil || IsNotInstalled(err) {
		t.Fatalf("changed program not reported: %v", err)
	}
	if code := errorCode(err); code != "engine.tool.modified" {
		t.Fatalf("code = %s", code)
	}
}

func TestLocateNoticesVersionChange(t *testing.T) {
	archive := zipWith(t, "folder/program", testProgram)
	f := newFixture(t, "windows/amd64", "tool.zip", archive, sumOf(archive))
	if _, err := f.installer.Install(context.Background(), ToolRestic); err != nil {
		t.Fatal(err)
	}
	tool := f.installer.Lock.Tools[ToolRestic]
	tool.Version = "9.9.9"
	f.installer.Lock.Tools[ToolRestic] = tool
	if _, err := f.installer.Locate(ToolRestic); !IsNotInstalled(err) {
		t.Fatalf("got %v", err)
	}
}

func TestParseLockRejectsBadData(t *testing.T) {
	good := fmt.Sprintf(`"tools": {"restic": {"version": "1", "repository": "a/b", "tag": "v1",
		"assets": {"linux/amd64": {"file": "f", "sha256": %q}}}}`, strings.Repeat("a", 64))
	if _, err := ParseLock([]byte(`{"schema_version": 1, ` + good + `}`)); err != nil {
		t.Fatalf("valid lock rejected: %v", err)
	}
	cases := map[string]string{
		"not json":       `{`,
		"unknown schema": `{"schema_version": 2, ` + good + `}`,
		"unknown field":  `{"schema_version": 1, "extra": 1, ` + good + `}`,
		"short hash":     `{"schema_version": 1, "tools": {"restic": {"version": "1", "repository": "a/b", "tag": "v1", "assets": {"x": {"file": "f", "sha256": "abc"}}}}}`,
		"no assets":      `{"schema_version": 1, "tools": {"restic": {"version": "1", "repository": "a/b", "tag": "v1"}}}`,
	}
	for name, input := range cases {
		if _, err := ParseLock([]byte(input)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLockAssetErrors(t *testing.T) {
	lock := &Lock{Tools: map[string]Tool{ToolRestic: {Assets: map[string]Asset{}}}}
	if _, _, err := lock.Asset("unknown", "linux/amd64"); err == nil {
		t.Error("unknown tool accepted")
	}
	if _, _, err := lock.Asset(ToolRestic, "plan9/mips"); err == nil {
		t.Error("unknown platform accepted")
	}
}

func TestDownloadHonoursContext(t *testing.T) {
	archive := zipWith(t, "folder/program", testProgram)
	f := newFixture(t, "windows/amd64", "tool.zip", archive, sumOf(archive))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.installer.Install(ctx, ToolRestic)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
