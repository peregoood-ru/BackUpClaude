//go:build windows

package discover

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
)

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		input, want string
		ok          bool
	}{
		{`d:\work\проект`, `D:\work\проект`, true},
		{`D:\work\проект\`, `D:\work\проект`, true},
		{`D:/work/проект`, `D:\work\проект`, true},
		{`/d/work/проект`, `D:\work\проект`, true},
		{`/c`, `C:\`, true},
		{`\\?\D:\work`, `D:\work`, true},
		{`/sessions/sandbox/outputs`, "", false},
		{`relative\path`, "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizePath(tc.input)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q: got %q %v", tc.input, got, ok)
		}
	}
}

func TestFilterIgnoresCaseOnWindows(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	filter := NewFilter(windowsManifest(t), fakeEnv(home))
	if _, skip := filter.Classify(strings.ToUpper(home)); skip != SkipHome {
		t.Errorf("skip = %q", skip)
	}
	if _, skip := filter.Classify(strings.ToUpper(filepath.Join(home, "AppData", "Local", "Temp", "x"))); skip != SkipSystem {
		t.Errorf("skip = %q", skip)
	}
}

func TestProjectsDifferingOnlyInCaseAreOne(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	project := filepath.Join(home, "Проект")
	fakehome.Transcript(t, home, "x", "a", project)
	fakehome.Transcript(t, home, "x", "b", strings.ToLower(project))
	state := &State{Version: stateVersion}
	scan(t, home, state)
	if len(state.Projects) != 1 || state.Projects[0].Sessions != 2 {
		t.Fatalf("projects = %+v", state.Projects)
	}
}

func TestLongNameResolvesWhatExists(t *testing.T) {
	short, ok := getLongPathName(`C:\`)
	if !ok || short == "" {
		t.Skip("the system does not answer")
	}
	if got := longName(`C:\no-such-folder~1\x`); got != `C:\no-such-folder~1\x` {
		t.Errorf("got %q", got)
	}
}
