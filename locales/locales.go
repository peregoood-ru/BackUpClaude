// Package locales embeds the message catalogs. Every user-facing string of
// the application lives in these catalogs.
package locales

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed en.json ru.json
var files embed.FS

var (
	mu     sync.Mutex
	loaded = map[string]map[string]string{}
)

// Languages lists the supported language codes.
func Languages() []string {
	return []string{"en", "ru"}
}

// Supported reports whether lang has a catalog.
func Supported(lang string) bool {
	for _, known := range Languages() {
		if known == lang {
			return true
		}
	}
	return false
}

// Catalog returns the messages of a supported language. The catalogs are
// embedded and covered by tests, so a broken one is a programming error.
func Catalog(lang string) map[string]string {
	mu.Lock()
	defer mu.Unlock()
	if msgs, ok := loaded[lang]; ok {
		return msgs
	}
	data, err := files.ReadFile(lang + ".json")
	if err != nil {
		panic(fmt.Sprintf("locales: no catalog for %q: %v", lang, err))
	}
	msgs := map[string]string{}
	if err := json.Unmarshal(data, &msgs); err != nil {
		panic(fmt.Sprintf("locales: broken catalog %q: %v", lang, err))
	}
	loaded[lang] = msgs
	return msgs
}
