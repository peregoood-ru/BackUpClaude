package sandbox

import "testing"

func TestNameAndSuffix(t *testing.T) {
	t.Setenv(Env, "")
	if Name() != "" || Suffix() != "" {
		t.Fatal("no sandbox expected")
	}
	t.Setenv(Env, " e2e1 ")
	if Name() != "e2e1" || Suffix() != "-e2e1" {
		t.Errorf("%q %q", Name(), Suffix())
	}
	for _, bad := range []string{`a\b`, "a b", "../x", "toolongsandboxname1", "имя"} {
		t.Setenv(Env, bad)
		if Name() != "" {
			t.Errorf("%q accepted", bad)
		}
	}
}
