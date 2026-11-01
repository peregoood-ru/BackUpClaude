package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

func validJSON(t *testing.T) string {
	t.Helper()
	path, err := json.Marshal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{"version": 1, "repositories": {"local": %s}}`, path)
}

func description(t *testing.T, err error) i18n.Description {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	return i18n.New("en").Describe(err)
}

func TestParseFillsDefaults(t *testing.T) {
	cfg, err := Parse([]byte(validJSON(t)), "c.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Schedule.ClaudeEveryHours != 1 || cfg.Schedule.ProjectsEveryHours != 24 {
		t.Errorf("schedule defaults: %+v", cfg.Schedule)
	}
	if cfg.Retention != (Retention{Hourly: 48, Daily: 30, Weekly: 12, Monthly: 24}) {
		t.Errorf("retention defaults: %+v", cfg.Retention)
	}
}

func TestParseAcceptsByteOrderMark(t *testing.T) {
	if _, err := Parse([]byte("\xef\xbb\xbf"+validJSON(t)), "c.json"); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejects(t *testing.T) {
	good := validJSON(t)
	local := good[strings.Index(good, `"repositories"`):]
	cases := []struct {
		name, input, code, contains string
	}{
		{"empty", "", "config.parse.empty", ""},
		{"truncated", `{"version": 1,`, "config.parse.truncated", ""},
		{"syntax", "{\n  \"version\": 1,,\n}", "config.parse.syntax", "line 2"},
		{"trailing", good + " {}", "config.parse.trailing", ""},
		{"unknown field", `{"version": 1, "retentoin": {}, ` + local, "config.parse.unknown_field", "retentoin"},
		{"nested unknown field", `{"version": 1, "repositories": {"remote": "x"}}`, "config.parse.unknown_field", "remote"},
		{"wrong type", `{"version": "1"}`, "config.parse.type.number", "version"},
		{"wrong list", `{"version": 1, "sources": {"extra": "x"}}`, "config.parse.type.list", "extra"},
		{"no version", `{` + local, "config.invalid", ""},
		{"future version", `{"version": 99, ` + local, "config.invalid", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.input), "c.json")
			d := description(t, err)
			if d.Code != tc.code {
				t.Fatalf("code = %q, want %q (%s)", d.Code, tc.code, d.Message)
			}
			if d.Hint == "" {
				t.Errorf("no hint for %q", d.Message)
			}
			if !strings.Contains(d.Message, tc.contains) {
				t.Errorf("%q does not contain %q", d.Message, tc.contains)
			}
		})
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	input := `{
	  "version": 1,
	  "language": "de",
	  "repositories": {"local": "relative/repo", "offsite": "ftp:nope"},
	  "schedule": {"claude_every_hours": 0, "projects_every_hours": 9999},
	  "retention": {"hourly": -1, "daily": 0, "weekly": 0, "monthly": 0},
	  "sources": {"extra": ["relative/dir"], "exclude": [" "]}
	}`
	_, err := Parse([]byte(input), "c.json")
	d := description(t, err)
	if d.Code != "config.invalid" {
		t.Fatalf("code = %q", d.Code)
	}
	if len(d.Details) != 9 {
		t.Fatalf("want 9 problems, got %d: %v", len(d.Details), d.Details)
	}
	again := description(t, func() error { _, e := Parse([]byte(input), "c.json"); return e }())
	if strings.Join(d.Details, "|") != strings.Join(again.Details, "|") {
		t.Error("problem order is not stable")
	}
}

func TestOffsiteAcceptsResticLocations(t *testing.T) {
	cfg := Default()
	cfg.Repositories.Local = t.TempDir()
	for _, location := range []string{"rclone:remote:backups", "sftp:user@host:/srv/repo", t.TempDir()} {
		cfg.Repositories.Offsite = location
		if err := cfg.validate("c.json"); err != nil {
			t.Errorf("%s rejected: %v", location, err)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if !IsMissing(err) {
		t.Fatalf("IsMissing = false for %v", err)
	}
	if IsMissing(errors.New("other")) {
		t.Error("IsMissing = true for an unrelated error")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "папка с пробелами")
	path := filepath.Join(dir, FileName)
	cfg := Default()
	cfg.Language = "ru"
	cfg.Repositories.Local = filepath.Join(dir, "repo")
	cfg.Sources.Extra = []string{"~/notes"}
	if err := Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Repositories.Local != cfg.Repositories.Local || loaded.Language != "ru" || len(loaded.Sources.Extra) != 1 {
		t.Errorf("round trip changed the settings: %+v", loaded)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestSaveRefusesInvalidSettings(t *testing.T) {
	cfg := Default()
	if err := Save(filepath.Join(t.TempDir(), FileName), &cfg); err == nil {
		t.Fatal("settings without a repository were saved")
	}
}

func TestResolveOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvHome, dir)
	layout, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if layout.Root != dir {
		t.Errorf("root = %q", layout.Root)
	}
	if filepath.Dir(layout.ConfigFile()) != dir || filepath.Dir(layout.BinDir()) != dir ||
		filepath.Dir(layout.LogsDir()) != dir || filepath.Dir(layout.CacheDir()) != dir ||
		filepath.Dir(layout.TempDir()) != dir {
		t.Errorf("paths outside the data directory: %+v", layout)
	}
}

func TestResolveRejectsRelativeOverride(t *testing.T) {
	t.Setenv(EnvHome, "relative/dir")
	if _, err := Resolve(); err == nil {
		t.Fatal("relative override accepted")
	}
}

func TestResolveByOSRules(t *testing.T) {
	t.Setenv(EnvHome, "")
	layout, err := Resolve()
	if err != nil {
		t.Skipf("no home directory here: %v", err)
	}
	if !filepath.IsAbs(layout.Root) || filepath.Base(layout.Root) != "BackUpClaude" {
		t.Errorf("root = %q", layout.Root)
	}
}
