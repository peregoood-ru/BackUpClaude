package engine

import (
	"archive/zip"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

const (
	defaultBaseURL  = "https://github.com"
	stateFileName   = "tools.json"
	maxDownloadSize = 512 << 20
	maxBinarySize   = 1 << 30
	downloadTimeout = 20 * time.Minute
)

// Installer puts pinned tools into a directory.
type Installer struct {
	Lock *Lock
	// Dir receives the programs, one folder per tool version.
	Dir string
	// Platform defaults to the current one.
	Platform string
	// BaseURL defaults to GitHub.
	BaseURL string
	// Client defaults to a client with a generous timeout.
	Client *http.Client
}

// Installed describes a program on disk.
type Installed struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

type stateEntry struct {
	Version string `json:"version"`
	// Path is relative to Installer.Dir, so the data directory can move.
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type state struct {
	SchemaVersion int                   `json:"schema_version"`
	Tools         map[string]stateEntry `json:"tools"`
}

func (in *Installer) platform() string {
	if in.Platform != "" {
		return in.Platform
	}
	return Platform()
}

func (in *Installer) statePath() string { return filepath.Join(in.Dir, stateFileName) }

func (in *Installer) readState() (*state, error) {
	st := &state{SchemaVersion: 1, Tools: map[string]stateEntry{}}
	data, err := os.ReadFile(in.statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, i18n.E("engine.state.unreadable", in.statePath()).Because(err)
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, i18n.E("engine.state.unreadable", in.statePath()).Because(err)
	}
	if st.Tools == nil {
		st.Tools = map[string]stateEntry{}
	}
	return st, nil
}

func (in *Installer) writeState(st *state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return i18n.E("engine.state.unwritable", in.statePath()).Because(err)
	}
	tmp := in.statePath() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return i18n.E("engine.state.unwritable", in.statePath()).Because(err)
	}
	if err := os.Rename(tmp, in.statePath()); err != nil {
		return i18n.E("engine.state.unwritable", in.statePath()).Because(err)
	}
	return nil
}

// Keys of the errors returned by Locate; doctor treats a tool that is simply
// not installed yet differently from a modified one.
const (
	keyToolMissing  = "engine.tool.missing"
	keyToolOutdated = "engine.tool.outdated"
)

// IsNotInstalled reports whether Locate failed because the pinned version has
// not been installed, as opposed to the file having been changed.
func IsNotInstalled(err error) bool {
	var e *i18n.Error
	return errors.As(err, &e) && (e.Key == keyToolMissing || e.Key == keyToolOutdated)
}

// Locate finds the pinned version of a tool and checks the file on disk
// against the hash recorded at install time.
func (in *Installer) Locate(name string) (*Installed, error) {
	tool, _, err := in.Lock.Asset(name, in.platform())
	if err != nil {
		return nil, err
	}
	st, err := in.readState()
	if err != nil {
		return nil, err
	}
	entry, ok := st.Tools[name]
	if !ok {
		return nil, i18n.E(keyToolMissing, name).Hint("engine.tool.install.hint")
	}
	if entry.Version != tool.Version {
		return nil, i18n.E(keyToolOutdated, name, entry.Version, tool.Version).Hint("engine.tool.install.hint")
	}
	path := filepath.Join(in.Dir, filepath.FromSlash(entry.Path))
	sum, err := hashFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, i18n.E(keyToolMissing, name).Hint("engine.tool.install.hint")
	}
	if err != nil {
		return nil, i18n.E("engine.tool.unreadable", path).Because(err)
	}
	if sum != entry.SHA256 {
		return nil, i18n.E("engine.tool.modified", name, path).Hint("engine.tool.install.hint")
	}
	return &Installed{Tool: name, Version: entry.Version, Path: path, SHA256: sum}, nil
}

// Install downloads the pinned version of a tool, checks the archive against
// tools.lock.json and unpacks the program. A verified copy is left alone.
func (in *Installer) Install(ctx context.Context, name string) (*Installed, error) {
	if have, err := in.Locate(name); err == nil {
		return have, nil
	}
	tool, asset, err := in.Lock.Asset(name, in.platform())
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return nil, i18n.E("engine.install.dir_failed", in.Dir).Because(err)
	}
	work, err := os.MkdirTemp(in.Dir, ".install-*")
	if err != nil {
		return nil, i18n.E("engine.install.dir_failed", in.Dir).Because(err)
	}
	defer os.RemoveAll(work)

	archive := filepath.Join(work, "archive")
	url := in.assetURL(tool, asset)
	if err := in.download(ctx, url, archive, asset); err != nil {
		return nil, err
	}
	unpacked := filepath.Join(work, "program")
	if err := extract(archive, asset, unpacked); err != nil {
		return nil, err
	}
	sum, err := hashFile(unpacked)
	if err != nil {
		return nil, i18n.E("engine.tool.unreadable", unpacked).Because(err)
	}

	folder := name + "-" + tool.Version
	destDir := filepath.Join(in.Dir, folder)
	if err := os.RemoveAll(destDir); err != nil {
		return nil, i18n.E("engine.install.dir_failed", destDir).Because(err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, i18n.E("engine.install.dir_failed", destDir).Because(err)
	}
	file := name + executableSuffix(in.platform())
	dest := filepath.Join(destDir, file)
	if err := os.Rename(unpacked, dest); err != nil {
		return nil, i18n.E("engine.install.dir_failed", dest).Because(err)
	}

	st, err := in.readState()
	if err != nil {
		return nil, err
	}
	st.Tools[name] = stateEntry{Version: tool.Version, Path: folder + "/" + file, SHA256: sum}
	if err := in.writeState(st); err != nil {
		return nil, err
	}
	return &Installed{Tool: name, Version: tool.Version, Path: dest, SHA256: sum}, nil
}

func (in *Installer) assetURL(tool Tool, asset Asset) string {
	base := in.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", strings.TrimRight(base, "/"), tool.Repository, tool.Tag, asset.File)
}

func (in *Installer) download(ctx context.Context, url, dest string, asset Asset) error {
	client := in.Client
	if client == nil {
		client = &http.Client{Timeout: downloadTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return i18n.E("engine.download.failed", url).Because(err)
	}
	req.Header.Set("User-Agent", "BackUpClaude")
	resp, err := client.Do(req)
	if err != nil {
		return i18n.E("engine.download.failed", url).Hint("engine.download.failed.hint").Because(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return i18n.E("engine.download.status", url, resp.StatusCode).Hint("engine.download.failed.hint")
	}

	out, err := os.Create(dest)
	if err != nil {
		return i18n.E("engine.install.dir_failed", dest).Because(err)
	}
	defer out.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(resp.Body, maxDownloadSize+1))
	if err != nil {
		return i18n.E("engine.download.failed", url).Hint("engine.download.failed.hint").Because(err)
	}
	if written > maxDownloadSize {
		return i18n.E("engine.download.too_large", url, maxDownloadSize)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != asset.SHA256 {
		return i18n.E("engine.download.hash_mismatch", asset.File, asset.SHA256, got).Hint("engine.download.hash_mismatch.hint")
	}
	return nil
}

// extract writes the program from a verified archive to dest. The output path
// never comes from the archive, so a crafted entry name cannot escape it.
func extract(archive string, asset Asset, dest string) error {
	switch {
	case strings.HasSuffix(asset.File, ".zip"):
		return extractZip(archive, asset, dest)
	case strings.HasSuffix(asset.File, ".bz2"):
		return extractBzip2(archive, dest)
	default:
		return i18n.E("engine.extract.unknown_format", asset.File)
	}
}

func extractZip(archive string, asset Asset, dest string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return i18n.E("engine.extract.failed", asset.File).Because(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != asset.Entry {
			continue
		}
		src, err := file.Open()
		if err != nil {
			return i18n.E("engine.extract.failed", asset.File).Because(err)
		}
		defer src.Close()
		return writeProgram(src, dest, asset.File)
	}
	return i18n.E("engine.extract.no_entry", asset.Entry, asset.File)
}

func extractBzip2(archive, dest string) error {
	src, err := os.Open(archive)
	if err != nil {
		return i18n.E("engine.extract.failed", archive).Because(err)
	}
	defer src.Close()
	return writeProgram(bzip2.NewReader(src), dest, filepath.Base(archive))
}

func writeProgram(src io.Reader, dest, label string) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return i18n.E("engine.extract.failed", label).Because(err)
	}
	written, err := io.Copy(out, io.LimitReader(src, maxBinarySize+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return i18n.E("engine.extract.failed", label).Because(err)
	}
	if written > maxBinarySize {
		return i18n.E("engine.download.too_large", label, maxBinarySize)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func executableSuffix(platform string) string {
	if strings.HasPrefix(platform, "windows/") {
		return ".exe"
	}
	return ""
}
