package i18n

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"ru":          "ru",
		"ru_RU.UTF-8": "ru",
		"ru-RU":       "ru",
		"EN":          "en",
		"en_US":       "en",
		"de_DE":       "",
		"":            "",
		"C":           "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectPrefersExplicitThenEnvironment(t *testing.T) {
	t.Setenv(EnvLang, "ru")
	if got := Detect("en"); got != "en" {
		t.Errorf("explicit language ignored: %q", got)
	}
	if got := Detect(""); got != "ru" {
		t.Errorf("environment language ignored: %q", got)
	}
}

func TestTranslatorMissingKeyReturnsKey(t *testing.T) {
	if got := New("ru").T("no.such.key"); got != "no.such.key" {
		t.Errorf("got %q", got)
	}
}

func TestDescribeKeepsCodeHintAndCause(t *testing.T) {
	err := E("config.missing", "c.json").Hint("config.missing.hint").Because(errors.New("boom")).Exit(7)
	d := New("en").Describe(err)
	if d.Code != "config.missing" || d.Exit != 7 || d.Hint == "" {
		t.Fatalf("unexpected description: %+v", d)
	}
	if !strings.Contains(d.Message, "c.json") || !strings.Contains(d.Message, "boom") {
		t.Errorf("message lost data: %q", d.Message)
	}
}

func TestDescribeUnknownError(t *testing.T) {
	d := New("en").Describe(errors.New("plain"))
	if d.Code != "error.unexpected" || d.Exit != 1 || !strings.Contains(d.Message, "plain") {
		t.Errorf("unexpected description: %+v", d)
	}
}

type localized struct{}

func (localized) Error() string     { return "raw" }
func (localized) Localized() *Error { return E("config.missing", "x") }

func TestDescribeUsesLocalizer(t *testing.T) {
	if d := New("en").Describe(localized{}); d.Code != "config.missing" {
		t.Errorf("got %+v", d)
	}
}

func TestNotReadyExitCode(t *testing.T) {
	d := New("ru").Describe(NotReady("backup", 2))
	if d.Exit != 3 || !strings.Contains(d.Message, "backup") {
		t.Errorf("got %+v", d)
	}
}
