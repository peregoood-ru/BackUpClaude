package fakehome

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCopyKeepsWhatTheInstallationHas(t *testing.T) {
	cases := []struct {
		install        Install
		classic, store bool
	}{
		{Both, true, true}, {Classic, true, false}, {Store, false, true}, {NoDesktop, false, false},
	}
	for _, tc := range cases {
		home := Copy(t, tc.install)
		_, err := os.Stat(filepath.Join(home, filepath.FromSlash(ClassicRoot)))
		if (err == nil) != tc.classic {
			t.Errorf("install %d: classic root: %v", tc.install, err)
		}
		_, err = os.Stat(filepath.Join(home, filepath.FromSlash(StoreRoot)))
		if (err == nil) != tc.store {
			t.Errorf("install %d: store root: %v", tc.install, err)
		}
	}
}

func TestCopyHasTheRepositoryMarker(t *testing.T) {
	home := Copy(t, Both)
	if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(Repository), ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(Tree(t, filepath.Join(home, ".claude")), ".credentials.json") {
		t.Error("the fixture must hold a fake login file")
	}
}

func TestLineEscapesThePath(t *testing.T) {
	if got := Line(`C:\Проект`); got == "" || !slices.Contains([]bool{true}, len(got) > 10) {
		t.Errorf("line = %q", got)
	}
}
