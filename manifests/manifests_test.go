package manifests

import "testing"

func TestLoadEveryOS(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		manifest, err := Load(goos)
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		if manifest.OS != goos || len(manifest.DesktopRoots) == 0 {
			t.Errorf("%s: %+v", goos, manifest)
		}
	}
}

func TestLoadUnknownOS(t *testing.T) {
	if _, err := Load("plan9"); err == nil {
		t.Fatal("expected an error")
	}
}
