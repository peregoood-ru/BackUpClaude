// Package update finds a newer release of the application, downloads it and
// checks it against the published checksums. It does not touch the running
// program: swapping the file is selfinstall's job.
package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

const (
	// DefaultRepo is where the releases are published.
	DefaultRepo = "peregoood-ru/BackUpClaude"
	// EnvToken names the variable with a GitHub token for a private repository.
	EnvToken = "BACKUPCLAUDE_GITHUB_TOKEN"
	// ChecksumsFile is the file GoReleaser publishes with every release.
	ChecksumsFile = "checksums.txt"

	defaultAPI      = "https://api.github.com"
	maxDownload     = 200 << 20
	maxProgramSize  = 400 << 20
	requestTimeout  = 15 * time.Minute
	maxListedAssets = 100
)

// Client talks to the GitHub API.
type Client struct {
	Repo string
	API  string
	// Token is optional. A private repository needs it; the value is sent only
	// to the API host and is never printed.
	Token string
	HTTP  *http.Client
}

// Asset is one file of a release.
type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Release is a published version.
type Release struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}

// Version is the tag without the leading v.
func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Asset finds a file of the release by name.
func (r *Release) Asset(name string) (Asset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return Asset{}, false
}

func (c *Client) api() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return defaultAPI
}

func (c *Client) repo() string {
	if c.Repo != "" {
		return c.Repo
	}
	return DefaultRepo
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: requestTimeout}
}

func (c *Client) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, i18n.E("update.request_failed").Because(err)
	}
	req.Header.Set("User-Agent", "BackUpClaude")
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		// The error of net/http names the address, never the token.
		return nil, i18n.E("update.network_failed").Hint("update.network_failed.hint").Because(err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	return nil, c.statusError(resp)
}

// statusError explains a refusal in words that say what to do. GitHub answers
// 404 for a private repository the caller may not see.
func (c *Client) statusError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return i18n.E("update.bad_token").Hint("update.bad_token.hint", EnvToken)
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return i18n.E("update.rate_limited").Hint("update.rate_limited.hint", EnvToken)
		}
		return c.noAccess()
	case http.StatusNotFound:
		return c.noAccess()
	}
	return i18n.E("update.status", resp.StatusCode).Hint("update.network_failed.hint")
}

func (c *Client) noAccess() error {
	if c.Token == "" {
		return i18n.E("update.no_access", c.repo()).Hint("update.no_access.hint", EnvToken)
	}
	return i18n.E("update.no_access_token", c.repo()).Hint("update.no_access_token.hint")
}

// Latest returns the newest published version. Drafts are never offered;
// prereleases only when asked for.
func (c *Client) Latest(ctx context.Context, withPrerelease bool) (*Release, error) {
	resp, err := c.get(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=30", c.api(), c.repo()), "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var releases []Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&releases); err != nil {
		return nil, i18n.E("update.bad_answer").Because(err)
	}
	var best *Release
	for i := range releases {
		release := &releases[i]
		if release.Draft || (release.Prerelease && !withPrerelease) {
			continue
		}
		if _, ok := parseVersion(release.Version()); !ok {
			continue
		}
		if best == nil || compare(release.Version(), best.Version()) > 0 {
			best = release
		}
	}
	if best == nil {
		return nil, i18n.E("update.no_releases", c.repo()).Hint("update.no_releases.hint")
	}
	if len(best.Assets) > maxListedAssets {
		best.Assets = best.Assets[:maxListedAssets]
	}
	return best, nil
}

// Fetch downloads an asset into dest and returns its SHA-256. The API address
// of the asset is used because the public download address does not work for
// a private repository.
func (c *Client) Fetch(ctx context.Context, asset Asset, dest string) (string, error) {
	resp, err := c.get(ctx, fmt.Sprintf("%s/repos/%s/releases/assets/%d", c.api(), c.repo(), asset.ID), "application/octet-stream")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, err := os.Create(dest)
	if err != nil {
		return "", i18n.E("update.write_failed", dest).Because(err)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(resp.Body, maxDownload+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", i18n.E("update.network_failed").Hint("update.network_failed.hint").Because(err)
	}
	if written > maxDownload {
		return "", i18n.E("update.too_large", asset.Name)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ArchiveName is the name of the archive for a platform, as GoReleaser names it.
func ArchiveName(version, goos, goarch string) string {
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("backupclaude_%s_%s_%s%s", strings.TrimPrefix(version, "v"), goos, goarch, extension)
}

// ParseChecksums reads checksums.txt: a hash, spaces, a file name per line.
func ParseChecksums(text string) map[string]string {
	sums := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return sums
}

// Verify compares the hash of a downloaded file with the published one.
func Verify(name, got string, sums map[string]string) error {
	want, ok := sums[name]
	if !ok {
		return i18n.E("update.no_checksum", name).Hint("update.no_checksum.hint")
	}
	if !strings.EqualFold(want, got) {
		return i18n.E("update.hash_mismatch", name, want, got).Hint("update.hash_mismatch.hint")
	}
	return nil
}

// HashFile returns the SHA-256 of a file.
func HashFile(path string) (string, error) {
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

// ExtractProgram writes the program out of an archive (zip or tar.gz), or
// copies a plain program file, to dest. The name inside the archive decides
// what is taken; the output path never comes from the archive.
func ExtractProgram(archive, programName, dest string) error {
	switch {
	case strings.HasSuffix(strings.ToLower(archive), ".zip"):
		return extractZip(archive, programName, dest)
	case strings.HasSuffix(strings.ToLower(archive), ".tar.gz"), strings.HasSuffix(strings.ToLower(archive), ".tgz"):
		return extractTarGz(archive, programName, dest)
	}
	source, err := os.Open(archive)
	if err != nil {
		return i18n.E("update.extract_failed", filepath.Base(archive)).Because(err)
	}
	defer source.Close()
	return writeProgram(source, dest, archive)
}

func extractZip(archive, programName, dest string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return i18n.E("update.extract_failed", filepath.Base(archive)).Because(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if filepath.Base(file.Name) != programName || file.FileInfo().IsDir() {
			continue
		}
		source, err := file.Open()
		if err != nil {
			return i18n.E("update.extract_failed", filepath.Base(archive)).Because(err)
		}
		defer source.Close()
		return writeProgram(source, dest, archive)
	}
	return i18n.E("update.no_program", programName, filepath.Base(archive))
}

func extractTarGz(archive, programName, dest string) error {
	file, err := os.Open(archive)
	if err != nil {
		return i18n.E("update.extract_failed", filepath.Base(archive)).Because(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		return i18n.E("update.extract_failed", filepath.Base(archive)).Because(err)
	}
	defer unzipped.Close()
	reader := tar.NewReader(unzipped)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return i18n.E("update.no_program", programName, filepath.Base(archive))
		}
		if err != nil {
			return i18n.E("update.extract_failed", filepath.Base(archive)).Because(err)
		}
		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == programName {
			return writeProgram(reader, dest, archive)
		}
	}
}

func writeProgram(source io.Reader, dest, label string) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return i18n.E("update.write_failed", dest).Because(err)
	}
	written, err := io.Copy(out, io.LimitReader(source, maxProgramSize+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return i18n.E("update.extract_failed", filepath.Base(label)).Because(err)
	}
	if written > maxProgramSize {
		return i18n.E("update.too_large", filepath.Base(label))
	}
	return nil
}

type version struct {
	major, minor, patch int
	pre                 []string
}

func parseVersion(text string) (version, bool) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "v")
	if i := strings.IndexByte(text, '+'); i >= 0 {
		text = text[:i]
	}
	core, pre, _ := strings.Cut(text, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var numbers [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version{}, false
		}
		numbers[i] = n
	}
	v := version{major: numbers[0], minor: numbers[1], patch: numbers[2]}
	if pre != "" {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

// Comparable reports whether text is a version number that can be compared; a
// development build is not.
func Comparable(text string) bool {
	_, ok := parseVersion(text)
	return ok
}

// Newer reports whether latest is a higher version than current. It is false
// when either cannot be read as a version.
func Newer(current, latest string) bool {
	if !Comparable(current) || !Comparable(latest) {
		return false
	}
	return compare(latest, current) > 0
}

// compare follows the SemVer rules: a release is higher than its prerelease.
func compare(a, b string) int {
	x, _ := parseVersion(a)
	y, _ := parseVersion(b)
	for _, pair := range [][2]int{{x.major, y.major}, {x.minor, y.minor}, {x.patch, y.patch}} {
		if pair[0] != pair[1] {
			if pair[0] > pair[1] {
				return 1
			}
			return -1
		}
	}
	switch {
	case len(x.pre) == 0 && len(y.pre) == 0:
		return 0
	case len(x.pre) == 0:
		return 1
	case len(y.pre) == 0:
		return -1
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		if c := comparePre(x.pre[i], y.pre[i]); c != 0 {
			return c
		}
	}
	return sign(len(x.pre) - len(y.pre))
}

func comparePre(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return sign(na - nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	names := []string{a, b}
	sort.Strings(names)
	if a == b {
		return 0
	}
	if names[0] == a {
		return -1
	}
	return 1
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}
