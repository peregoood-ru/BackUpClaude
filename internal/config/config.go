// Package config loads and validates the user's settings. team.json joins it
// at stage 9.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/locales"
)

// CurrentVersion is the config.json schema this build reads and writes.
const CurrentVersion = 1

// Limits of the schedule settings, in hours.
const (
	maxClaudeEveryHours   = 168
	maxProjectsEveryHours = 720
)

// Config is the content of config.json.
type Config struct {
	Version      int          `json:"version"`
	Language     string       `json:"language,omitempty"`
	Repositories Repositories `json:"repositories"`
	Schedule     Schedule     `json:"schedule"`
	Retention    Retention    `json:"retention"`
	Sources      Sources      `json:"sources"`
}

// Repositories says where the two encrypted copies live.
type Repositories struct {
	// Local is an absolute path outside any sync folder.
	Local string `json:"local"`
	// Offsite is any restic location, for example rclone:remote:path. It stays
	// empty until the user sets up the external copy.
	Offsite string `json:"offsite,omitempty"`
}

// Schedule sets how often each profile runs.
type Schedule struct {
	ClaudeEveryHours   int `json:"claude_every_hours"`
	ProjectsEveryHours int `json:"projects_every_hours"`
}

// Retention is how many snapshots of each kind to keep.
type Retention struct {
	Hourly  int `json:"hourly"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

// Sources adds to and subtracts from what discovery finds.
type Sources struct {
	Extra   []string `json:"extra,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

// Default returns the settings used for everything config.json leaves out.
func Default() Config {
	return Config{
		Version:   CurrentVersion,
		Schedule:  Schedule{ClaudeEveryHours: 1, ProjectsEveryHours: 24},
		Retention: Retention{Hourly: 48, Daily: 30, Weekly: 12, Monthly: 24},
	}
}

// Load reads and strictly checks a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, i18n.E(keyMissing, path).Hint("config.missing.hint")
	}
	if err != nil {
		return nil, i18n.E("config.read_failed", path).Hint("config.read_failed.hint").Because(err)
	}
	return Parse(data, path)
}

const keyMissing = "config.missing"

// IsMissing reports whether err means there is no config file yet.
func IsMissing(err error) bool {
	var e *i18n.Error
	return errors.As(err, &e) && e.Key == keyMissing
}

// Parse decodes config data strictly; name appears in error messages.
func Parse(data []byte, name string) (*Config, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	cfg := Default()
	cfg.Version = 0

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, decodeError(err, data, name)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		line, col := position(data, dec.InputOffset())
		return nil, i18n.E("config.parse.trailing", name, line, col).Hint("config.parse.hint")
	}
	if err := cfg.validate(name); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var quoted = regexp.MustCompile(`"([^"]+)"`)

func decodeError(err error, data []byte, name string) error {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.EOF):
		return i18n.E("config.parse.empty", name).Hint("config.parse.hint")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return i18n.E("config.parse.truncated", name).Hint("config.parse.hint")
	case errors.As(err, &syntaxErr):
		line, col := position(data, syntaxErr.Offset)
		return i18n.E("config.parse.syntax", name, line, col).Hint("config.parse.hint")
	case errors.As(err, &typeErr):
		line, col := position(data, typeErr.Offset)
		return i18n.E("config.parse.type."+typeName(typeErr.Type), name, line, col, typeErr.Field).Hint("config.parse.hint")
	}
	if field := quoted.FindStringSubmatch(err.Error()); field != nil && strings.Contains(err.Error(), "unknown") {
		return i18n.E("config.parse.unknown_field", name, field[1]).Hint("config.unknown_field.hint")
	}
	return i18n.E("config.parse.failed", name).Hint("config.parse.hint").Because(err)
}

func typeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int, reflect.Int64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Slice:
		return "list"
	case reflect.Struct, reflect.Map:
		return "object"
	default:
		return "other"
	}
}

// position converts a byte offset to a 1-based line and column.
func position(data []byte, offset int64) (line, col int) {
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	before := data[:offset]
	line = bytes.Count(before, []byte("\n")) + 1
	col = len(before) - bytes.LastIndexByte(before, '\n')
	return line, col
}

// offsiteSchemes are the restic backends accepted as repositories.offsite.
var offsiteSchemes = []string{"rclone:", "sftp:", "rest:", "s3:", "b2:", "azure:", "gs:", "swift:"}

func (c *Config) validate(name string) error {
	var problems []*i18n.Error
	add := func(key string, args ...any) { problems = append(problems, i18n.E(key, args...)) }

	switch c.Version {
	case 0:
		add("config.version.missing", CurrentVersion)
	case CurrentVersion:
	default:
		add("config.version.unsupported", c.Version, CurrentVersion)
	}
	if c.Language != "" && !locales.Supported(c.Language) {
		add("config.language.unsupported", c.Language, strings.Join(locales.Languages(), ", "))
	}
	switch {
	case strings.TrimSpace(c.Repositories.Local) == "":
		add("config.local.missing")
	case !filepath.IsAbs(c.Repositories.Local):
		add("config.local.not_absolute", c.Repositories.Local)
	}
	if off := c.Repositories.Offsite; off != "" && !validOffsite(off) {
		add("config.offsite.invalid", off, strings.Join(offsiteSchemes, " "))
	}
	if v := c.Schedule.ClaudeEveryHours; v < 1 || v > maxClaudeEveryHours {
		add("config.range", "schedule.claude_every_hours", v, 1, maxClaudeEveryHours)
	}
	if v := c.Schedule.ProjectsEveryHours; v < 1 || v > maxProjectsEveryHours {
		add("config.range", "schedule.projects_every_hours", v, 1, maxProjectsEveryHours)
	}
	r := c.Retention
	for _, item := range []struct {
		field string
		value int
	}{
		{"retention.hourly", r.Hourly}, {"retention.daily", r.Daily},
		{"retention.weekly", r.Weekly}, {"retention.monthly", r.Monthly},
	} {
		if item.value < 0 {
			add("config.negative", item.field, item.value)
		}
	}
	if r.Hourly+r.Daily+r.Weekly+r.Monthly <= 0 {
		add("config.retention.nothing")
	}
	for i, path := range c.Sources.Extra {
		if strings.TrimSpace(path) == "" || !(filepath.IsAbs(path) || strings.HasPrefix(path, "~")) {
			add("config.extra.invalid", i+1, path)
		}
	}
	for i, pattern := range c.Sources.Exclude {
		if strings.TrimSpace(pattern) == "" {
			add("config.exclude.empty", i+1)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return &i18n.Error{Key: "config.invalid", Args: []any{name}, HintKey: "config.invalid.hint", Details: problems}
}

func validOffsite(location string) bool {
	if filepath.IsAbs(location) {
		return true
	}
	for _, scheme := range offsiteSchemes {
		if strings.HasPrefix(location, scheme) && len(location) > len(scheme) {
			return true
		}
	}
	return false
}

// Save validates cfg and writes it atomically, readable only by the user.
func Save(path string, cfg *Config) error {
	if err := cfg.validate(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return i18n.E("config.write_failed", path).Because(err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return i18n.E("config.write_failed", path).Because(err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return i18n.E("config.write_failed", path).Because(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return i18n.E("config.write_failed", path).Because(err)
	}
	if err := tmp.Close(); err != nil {
		return i18n.E("config.write_failed", path).Because(err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return i18n.E("config.write_failed", path).Because(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return i18n.E("config.write_failed", path).Because(err)
	}
	return nil
}
