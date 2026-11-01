// Package restore brings data back and proves it came back intact. Comparing
// trees works from stage 1; the restore flows arrive at stage 6.
package restore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// FileHash is one file of a tree.
type FileHash struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Tree is the content of a folder, sorted by path.
type Tree struct {
	Files  []FileHash `json:"-"`
	Digest string     `json:"digest"`
	Bytes  int64      `json:"bytes"`
}

// HashTree hashes every regular file under root. Paths are relative and use
// forward slashes, so trees from different locations compare equal.
func HashTree(root string) (*Tree, error) {
	tree := &Tree{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum, size, err := hashFile(path)
		if err != nil {
			return err
		}
		tree.Files = append(tree.Files, FileHash{Path: filepath.ToSlash(rel), Size: size, SHA256: sum})
		tree.Bytes += size
		return nil
	})
	if err != nil {
		return nil, i18n.E("restore.hash_failed", root).Because(err)
	}
	sort.Slice(tree.Files, func(i, j int) bool { return tree.Files[i].Path < tree.Files[j].Path })
	digest := sha256.New()
	for _, file := range tree.Files {
		fmt.Fprintf(digest, "%s\x00%d\x00%s\n", file.Path, file.Size, file.SHA256)
	}
	tree.Digest = hex.EncodeToString(digest.Sum(nil))
	return tree, nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// Difference is a file that does not match between two trees.
type Difference struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Reasons of a Difference.
const (
	ReasonMissing = "missing"
	ReasonExtra   = "extra"
	ReasonChanged = "changed"
)

// Compare lists how got differs from want.
func Compare(want, got *Tree) []Difference {
	wanted := make(map[string]FileHash, len(want.Files))
	for _, file := range want.Files {
		wanted[file.Path] = file
	}
	var differences []Difference
	for _, file := range got.Files {
		expected, ok := wanted[file.Path]
		switch {
		case !ok:
			differences = append(differences, Difference{file.Path, ReasonExtra})
		case expected != file:
			differences = append(differences, Difference{file.Path, ReasonChanged})
		}
		delete(wanted, file.Path)
	}
	for path := range wanted {
		differences = append(differences, Difference{path, ReasonMissing})
	}
	sort.Slice(differences, func(i, j int) bool { return differences[i].Path < differences[j].Path })
	return differences
}

// RestoredPath returns where restic puts source when restoring a snapshot into
// target: the full path is kept, with the drive letter as a folder.
func RestoredPath(target, source string) string {
	volume := filepath.VolumeName(source)
	rest := strings.TrimPrefix(source, volume)
	return filepath.Join(target, strings.TrimSuffix(volume, ":"), rest)
}

const (
	sampleRandomBytes = 1 << 20
	sampleDeepLevels  = 24
)

// GenerateSample fills dir with files that exercise what backups trip over:
// Cyrillic names, spaces, empty files, incompressible data and a path longer
// than the classic Windows limit of 260 characters.
func GenerateSample(dir string) error {
	random := make([]byte, sampleRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return i18n.E("restore.sample_failed", dir).Because(err)
	}
	deep := make([]string, 0, sampleDeepLevels+1)
	for level := range sampleDeepLevels {
		deep = append(deep, fmt.Sprintf("глубокая папка %02d", level))
	}
	deep = append(deep, "дно.txt")

	files := map[string][]byte{
		"readme.txt":      []byte("BackUpClaude selftest\n"),
		"пустой файл.txt": {},
		filepath.Join("Папка с пробелами", "заметка.md"): []byte("# Проверка кириллицы\n"),
		filepath.Join("data", "random.bin"):              random,
		filepath.Join(deep...):                           []byte("deep\n"),
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return i18n.E("restore.sample_failed", dir).Because(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return i18n.E("restore.sample_failed", dir).Because(err)
		}
	}
	return nil
}
