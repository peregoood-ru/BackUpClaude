package locales

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var verb = regexp.MustCompile(`%(\[\d+\])?[a-z]`)

func TestCatalogsHaveTheSameKeys(t *testing.T) {
	en, ru := Catalog("en"), Catalog("ru")
	for key := range en {
		if _, ok := ru[key]; !ok {
			t.Errorf("ru.json lacks %s", key)
		}
	}
	for key := range ru {
		if _, ok := en[key]; !ok {
			t.Errorf("en.json lacks %s", key)
		}
	}
}

// verbs returns the sorted verbs without argument indexes: the count and kinds
// must agree even when a language reorders them.
func verbs(message string) string {
	found := verb.FindAllString(message, -1)
	for i, item := range found {
		found[i] = item[len(item)-1:]
	}
	sort.Strings(found)
	return strings.Join(found, "")
}

func TestPlaceholdersAgree(t *testing.T) {
	en, ru := Catalog("en"), Catalog("ru")
	for key, message := range en {
		if strings.TrimSpace(message) == "" || strings.TrimSpace(ru[key]) == "" {
			t.Errorf("%s is empty", key)
		}
		if verbs(message) != verbs(ru[key]) {
			t.Errorf("%s: placeholders differ: %q vs %q", key, message, ru[key])
		}
	}
}

func TestSupported(t *testing.T) {
	if !Supported("ru") || !Supported("en") || Supported("de") {
		t.Error("Supported gives wrong answers")
	}
}

// keyUse matches translation calls with a literal key.
var keyUse = regexp.MustCompile(`(?:\.T\(|\bE\(|\.Hint\(|i18n\.E\()"([a-z_]+(?:\.[a-z_0-9]+)+)"`)

// TestCodeUsesOnlyKnownKeys walks the sources: a typo in a key must not reach
// the user as a raw key.
func TestCodeUsesOnlyKnownKeys(t *testing.T) {
	en := Catalog("en")
	root := filepath.Join("..")
	used := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "dist" || entry.Name() == "testdata") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range keyUse.FindAllSubmatch(source, -1) {
			used++
			if _, ok := en[string(match[1])]; !ok {
				t.Errorf("%s uses unknown key %s", path, match[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if used < 50 {
		t.Errorf("the scan found only %d keys, the pattern is probably broken", used)
	}
}
