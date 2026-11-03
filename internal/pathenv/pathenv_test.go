package pathenv

import (
	"strings"
	"testing"
)

func noExpand(s string) string { return s }

func TestAddAppendsOnce(t *testing.T) {
	got, changed := add(`C:\Tools;C:\Go\bin`, `C:\Users\Ivan\.backupclaude\bin`, noExpand)
	if !changed || got != `C:\Tools;C:\Go\bin;C:\Users\Ivan\.backupclaude\bin` {
		t.Fatalf("%q %v", got, changed)
	}
	again, changed := add(got, `c:\users\ivan\.BACKUPCLAUDE\bin\`, noExpand)
	if changed || again != got {
		t.Errorf("added twice: %q", again)
	}
}

func TestAddHandlesEmptyAndTrailingSeparator(t *testing.T) {
	if got, changed := add("", `D:\bin`, noExpand); !changed || got != `D:\bin` {
		t.Errorf("%q %v", got, changed)
	}
	if got, _ := add(`C:\a;`, `D:\bin`, noExpand); got != `C:\a;D:\bin` {
		t.Errorf("%q", got)
	}
}

func TestEntriesWithVariablesAreComparedExpanded(t *testing.T) {
	expand := func(s string) string { return strings.ReplaceAll(s, "%USERPROFILE%", `C:\Users\Ivan`) }
	current := `%USERPROFILE%\.backupclaude\bin;C:\Tools`
	if _, changed := add(current, `C:\Users\Ivan\.backupclaude\bin`, expand); changed {
		t.Error("an entry with a variable was not recognised")
	}
	got, changed := remove(current, `C:\Users\Ivan\.backupclaude\bin`, expand)
	if !changed || got != `C:\Tools` {
		t.Errorf("%q %v", got, changed)
	}
}

func TestRemoveKeepsTheRest(t *testing.T) {
	got, changed := remove(`C:\a;D:\bin;C:\b`, `d:\BIN`, noExpand)
	if !changed || got != `C:\a;C:\b` {
		t.Errorf("%q %v", got, changed)
	}
	if got, changed := remove(`C:\a`, `D:\bin`, noExpand); changed || got != `C:\a` {
		t.Errorf("%q %v", got, changed)
	}
	if !contains(`C:\a;D:\bin`, `D:\bin`, noExpand) || contains(`C:\a`, `D:\bin`, noExpand) {
		t.Error("contains is wrong")
	}
}

func TestEmptyDirNeverMatches(t *testing.T) {
	if same("", "") || same(" ", `"`) {
		t.Error("empty folders matched")
	}
}
