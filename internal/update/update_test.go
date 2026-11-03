package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "0.1.1", true},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.9", false},
		{"0.1.0-rc.1", "0.1.0", true},
		{"0.1.0", "0.1.0-rc.1", false},
		{"0.1.0-rc.1", "0.1.0-rc.2", true},
		{"0.1.0-rc.2", "0.1.0-rc.10", true},
		{"0.0.0-stage6", "0.1.0", true},
		{"0.1.0-alpha", "0.1.0-beta", true},
		{"1.0.0", "v1.0.1", true},
		{"dev", "0.1.0", false},
		{"0.1.0", "garbage", false},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
	if Comparable("dev") || !Comparable("0.0.0-stage6") {
		t.Error("Comparable is wrong")
	}
}

func TestArchiveNameAndChecksums(t *testing.T) {
	if got := ArchiveName("v0.1.0", "windows", "amd64"); got != "backupclaude_0.1.0_windows_amd64.zip" {
		t.Error(got)
	}
	if got := ArchiveName("0.1.0", "linux", "arm64"); got != "backupclaude_0.1.0_linux_arm64.tar.gz" {
		t.Error(got)
	}
	hash := strings.Repeat("ab", 32)
	sums := ParseChecksums(hash + "  backupclaude_0.1.0_windows_amd64.zip\nnot a line\n" + strings.ToUpper(hash) + " *other.tar.gz\n")
	if sums["backupclaude_0.1.0_windows_amd64.zip"] != hash || sums["other.tar.gz"] != hash || len(sums) != 2 {
		t.Errorf("%v", sums)
	}
	if err := Verify("x.zip", hash, sums); err == nil || !isKey(err, "update.no_checksum") {
		t.Errorf("a file without a checksum passed: %v", err)
	}
	if err := Verify("other.tar.gz", strings.Repeat("00", 32), sums); !isKey(err, "update.hash_mismatch") {
		t.Errorf("a wrong hash passed: %v", err)
	}
	if err := Verify("other.tar.gz", hash, sums); err != nil {
		t.Error(err)
	}
}

func isKey(err error, key string) bool {
	var e *i18n.Error
	return errors.As(err, &e) && e.Key == key
}

func server(t *testing.T, token string, releases any, files map[string]string) *Client {
	t.Helper()
	mux := http.NewServeMux()
	check := func(w http.ResponseWriter, r *http.Request) bool {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.NotFound(w, r)
			return false
		}
		return true
	}
	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		if check(w, r) {
			_ = json.NewEncoder(w).Encode(releases)
		}
	})
	mux.HandleFunc("/repos/o/r/releases/assets/", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		if r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, "wrong accept", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(files[strings.TrimPrefix(r.URL.Path, "/repos/o/r/releases/assets/")]))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Client{Repo: "o/r", API: srv.URL, Token: token, HTTP: srv.Client()}
}

func TestLatestPicksTheHighestPublishedRelease(t *testing.T) {
	client := server(t, "", []Release{
		{Tag: "v0.2.0", Draft: true},
		{Tag: "v0.1.1-rc.1", Prerelease: true},
		{Tag: "v0.1.0", Assets: []Asset{{ID: 1, Name: "a.zip"}}},
		{Tag: "v0.0.9"},
		{Tag: "nightly"},
	}, nil)
	release, err := client.Latest(context.Background(), false)
	if err != nil || release.Tag != "v0.1.0" || release.Version() != "0.1.0" {
		t.Fatalf("%+v %v", release, err)
	}
	if _, ok := release.Asset("a.zip"); !ok {
		t.Error("asset not found")
	}
	withPre, err := client.Latest(context.Background(), true)
	if err != nil || withPre.Tag != "v0.1.1-rc.1" {
		t.Fatalf("%+v %v", withPre, err)
	}
}

func TestPrivateRepositoryWithoutTokenGivesAnUnderstandableError(t *testing.T) {
	client := server(t, "secret-token", []Release{{Tag: "v0.1.0"}}, nil)
	client.Token = ""
	_, err := client.Latest(context.Background(), false)
	if !isKey(err, "update.no_access") {
		t.Fatalf("%v", err)
	}
	var e *i18n.Error
	errors.As(err, &e)
	if e.HintKey != "update.no_access.hint" || len(e.HintArgs) != 1 || e.HintArgs[0] != EnvToken {
		t.Errorf("the hint does not name the token variable: %+v", e)
	}
	client.Token = "wrong"
	if _, err := client.Latest(context.Background(), false); !isKey(err, "update.no_access_token") {
		t.Errorf("wrong token: %v", err)
	}
	client.Token = "secret-token"
	if _, err := client.Latest(context.Background(), false); err != nil {
		t.Errorf("right token: %v", err)
	}
}

func TestNoReleasesAndBrokenAnswer(t *testing.T) {
	client := server(t, "", []Release{}, nil)
	if _, err := client.Latest(context.Background(), false); !isKey(err, "update.no_releases") {
		t.Errorf("%v", err)
	}
	client = server(t, "", "not a list", nil)
	if _, err := client.Latest(context.Background(), false); !isKey(err, "update.bad_answer") {
		t.Errorf("%v", err)
	}
}

func TestNoNetworkMentionsNoToken(t *testing.T) {
	client := &Client{Repo: "o/r", API: "http://127.0.0.1:1", Token: "secret-token"}
	_, err := client.Latest(context.Background(), false)
	if !isKey(err, "update.network_failed") {
		t.Fatalf("%v", err)
	}
	if strings.Contains(i18n.New("en").Describe(err).Message, "secret-token") {
		t.Error("the token leaked into the error")
	}
}

func TestFetchReturnsTheHash(t *testing.T) {
	client := server(t, "", nil, map[string]string{"7": "payload"})
	dest := filepath.Join(t.TempDir(), "file")
	sum, err := client.Fetch(context.Background(), Asset{ID: 7, Name: "p"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := HashFile(dest)
	if sum != want || sum == "" {
		t.Errorf("%s vs %s", sum, want)
	}
}

func makeZip(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.zip")
	file, _ := os.Create(path)
	w := zip.NewWriter(file)
	for _, entry := range []string{"LICENSE", name} {
		f, _ := w.Create(entry)
		_, _ = f.Write([]byte(content + " from " + entry))
	}
	_ = w.Close()
	_ = file.Close()
	return path
}

func TestExtractProgram(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractProgram(makeZip(t, "backupclaude.exe", "x"), "backupclaude.exe", dest); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "x from backupclaude.exe" {
		t.Errorf("%q", data)
	}
	if err := ExtractProgram(makeZip(t, "other.exe", "x"), "backupclaude.exe", dest); !isKey(err, "update.no_program") {
		t.Errorf("%v", err)
	}

	tarPath := filepath.Join(t.TempDir(), "a.tar.gz")
	file, _ := os.Create(tarPath)
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	body := []byte("tar body")
	_ = tw.WriteHeader(&tar.Header{Name: "dir/backupclaude", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	_ = file.Close()
	dest2 := filepath.Join(t.TempDir(), "out2")
	if err := ExtractProgram(tarPath, "backupclaude", dest2); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest2); string(data) != "tar body" {
		t.Errorf("%q", data)
	}

	plain := filepath.Join(t.TempDir(), "plain.exe")
	_ = os.WriteFile(plain, []byte("plain"), 0o755)
	dest3 := filepath.Join(t.TempDir(), "out3")
	if err := ExtractProgram(plain, "backupclaude.exe", dest3); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest3); string(data) != "plain" {
		t.Errorf("%q", data)
	}
}

func TestArchiveEntryNameNeverChoosesTheOutputPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evil.zip")
	file, _ := os.Create(path)
	w := zip.NewWriter(file)
	f, _ := w.Create("../../escape/backupclaude.exe")
	_, _ = f.Write([]byte("evil"))
	_ = w.Close()
	_ = file.Close()
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractProgram(path, "backupclaude.exe", dest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "evil" {
		t.Errorf("content went elsewhere: %q", data)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "..", "..", "escape")); err == nil {
		t.Error("an entry wrote outside the destination")
	}
}
