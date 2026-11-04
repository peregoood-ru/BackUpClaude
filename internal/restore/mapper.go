package restore

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Pair moves everything below From to To. Both are paths of this OS.
type Pair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Mapper rewrites the paths of another computer, or of this one before the
// user name or a drive letter changed, into paths of this computer. A path is
// moved by the longest pair that holds it; a blocked folder has no place here.
//
// The rules follow claude-moove (see NOTICE): folders compare without regard
// to case, a pair only matches whole folder names ("C:\Users\Al" does not move
// "C:\Users\Alex"), and inside Claude's own files a path is found in three
// spellings: as is, with doubled backslashes inside JSON, and with forward
// slashes.
type Mapper struct {
	pairs   []Pair
	blocked []string
}

// NewMapper drops pairs that change nothing.
func NewMapper(pairs []Pair) *Mapper {
	m := &Mapper{}
	for _, pair := range pairs {
		from, to := filepath.Clean(pair.From), filepath.Clean(pair.To)
		if from == to {
			continue
		}
		m.pairs = append(m.pairs, Pair{from, to})
	}
	sort.SliceStable(m.pairs, func(i, j int) bool { return len(m.pairs[i].From) > len(m.pairs[j].From) })
	return m
}

// Block makes Map refuse everything below folder: there is no place for it on
// this computer, for example the desktop application is not installed.
func (m *Mapper) Block(folder string) {
	m.blocked = append(m.blocked, filepath.Clean(folder))
}

// Pairs returns the pairs in the order they are tried.
func (m *Mapper) Pairs() []Pair { return m.pairs }

// Identity reports whether no path changes.
func (m *Mapper) Identity() bool { return len(m.pairs) == 0 }

// Map returns where path belongs on this computer. ok is false for a path in a
// blocked folder. A pair more specific than a block wins over it.
func (m *Mapper) Map(path string) (mapped string, ok bool) {
	path = filepath.Clean(path)
	best := -1
	for i, pair := range m.pairs {
		if WithinLocal(pair.From, path) {
			best = i
			break
		}
	}
	for _, folder := range m.blocked {
		if WithinLocal(folder, path) && (best < 0 || len(folder) > len(m.pairs[best].From)) {
			return "", false
		}
	}
	if best < 0 {
		return path, true
	}
	pair := m.pairs[best]
	rest := tail(pair.From, path)
	if rest == "" {
		return pair.To, true
	}
	return filepath.Join(pair.To, rest), true
}

// tail returns what path has below parent, which holds it. Parts are counted
// rather than bytes: changing the case of a letter can change its length.
func tail(parent, path string) string {
	parentParts := splitPath(parent)
	parts := splitPath(path)
	return filepath.Join(parts[len(parentParts):]...)
}

func splitPath(path string) []string {
	var parts []string
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// ProjectFolder renames a folder of transcripts for the moved working folder:
// the old folder's encoded name at the start becomes the new one's. A name that
// does not start with a moved folder stays.
func (m *Mapper) ProjectFolder(name string) string {
	for _, pair := range m.pairs {
		from := ProjectFolder(pair.From)
		if len(name) < len(from) || !strings.EqualFold(name[:len(from)], from) {
			continue
		}
		if len(name) > len(from) && isAlphanumeric(name[len(from)]) {
			continue // a longer folder name, not this folder
		}
		return ProjectFolder(pair.To) + name[len(from):]
	}
	return name
}

func isAlphanumeric(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// RewriteText moves the paths written inside a text: settings, the list of
// projects in ~/.claude.json, the application's descriptions of sessions.
// Transcripts are left as they are: they are history, and Claude Code finds
// them by the folder they lie in, which ProjectFolder renames.
func (m *Mapper) RewriteText(text string) string {
	if m.Identity() {
		return text
	}
	type form struct{ from, to string }
	var forms []form
	for _, pair := range m.pairs {
		forms = append(forms,
			form{pair.From, pair.To},
			form{strings.ReplaceAll(pair.From, `\`, `\\`), strings.ReplaceAll(pair.To, `\`, `\\`)},
			form{strings.ReplaceAll(pair.From, `\`, "/"), strings.ReplaceAll(pair.To, `\`, "/")},
		)
	}
	sort.SliceStable(forms, func(i, j int) bool { return len(forms[i].from) > len(forms[j].from) })
	replacement := map[string]string{}
	alternatives := make([]string, 0, len(forms))
	for _, f := range forms {
		key := strings.ToLower(f.from)
		if _, seen := replacement[key]; seen {
			continue
		}
		replacement[key] = f.to
		alternatives = append(alternatives, regexp.QuoteMeta(f.from))
	}
	// A match must end where a folder name ends, and Go's regexp has no look
	// ahead: the character after the match is taken along and given back.
	// Letters of any alphabet count: a user folder may have a Cyrillic name.
	pattern := regexp.MustCompile(`(?i)(?:` + strings.Join(alternatives, "|") + `)([^\p{L}\p{N}_.-]|\.[^\p{L}\p{N}_]|$|\.$)`)
	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := pattern.FindStringSubmatch(match)
		end := sub[1]
		found := match[:len(match)-len(end)]
		return replacement[strings.ToLower(found)] + end
	})
}
