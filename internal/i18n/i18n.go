// Package i18n renders user-facing text from the locales catalogs and carries
// translatable errors with a hint on what to do next.
package i18n

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/locales"
)

// DefaultLang is used when no language can be detected.
const DefaultLang = "en"

// EnvLang overrides language detection.
const EnvLang = "BACKUPCLAUDE_LANG"

// Translator looks up messages for one language.
type Translator struct {
	lang     string
	msgs     map[string]string
	fallback map[string]string
}

// New returns a Translator. An unknown language falls back to DefaultLang.
func New(lang string) *Translator {
	lang = Normalize(lang)
	if lang == "" {
		lang = DefaultLang
	}
	return &Translator{
		lang:     lang,
		msgs:     locales.Catalog(lang),
		fallback: locales.Catalog(DefaultLang),
	}
}

// Lang returns the language code in use.
func (t *Translator) Lang() string { return t.lang }

// T returns the message for key. A missing key returns the key itself, which
// the locales tests turn into a failure.
func (t *Translator) T(key string, args ...any) string {
	format, ok := t.msgs[key]
	if !ok {
		format, ok = t.fallback[key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// Normalize maps a locale such as "ru_RU.UTF-8" to a supported language code,
// or "" when it is not supported.
func Normalize(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "_-."); i >= 0 {
		lang = lang[:i]
	}
	if locales.Supported(lang) {
		return lang
	}
	return ""
}

// Detect picks the language: the explicit value, then the first candidate from
// the environment, then the OS setting, then DefaultLang.
func Detect(explicit string) string {
	candidates := []string{explicit, os.Getenv(EnvLang), os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG"), osLanguage()}
	for _, candidate := range candidates {
		if lang := Normalize(candidate); lang != "" {
			return lang
		}
	}
	return DefaultLang
}

// Error is an error whose text comes from the catalogs.
type Error struct {
	Key      string
	Args     []any
	HintKey  string
	HintArgs []any
	ExitCode int
	Cause    error
	Details  []*Error
}

// E creates an Error for a catalog key.
func E(key string, args ...any) *Error {
	return &Error{Key: key, Args: args}
}

// Hint sets what the user should do about the error.
func (e *Error) Hint(key string, args ...any) *Error {
	e.HintKey, e.HintArgs = key, args
	return e
}

// Exit sets the process exit code.
func (e *Error) Exit(code int) *Error {
	e.ExitCode = code
	return e
}

// Because records the underlying error; its text is appended to the message.
func (e *Error) Because(err error) *Error {
	e.Cause = err
	return e
}

// Error implements error with English text. User-facing code uses Describe.
func (e *Error) Error() string {
	return New(DefaultLang).Describe(e).Message
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// Localizer is implemented by errors of other packages that know how to
// present themselves.
type Localizer interface {
	Localized() *Error
}

// Description is an error ready to show to a user.
type Description struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Hint    string   `json:"hint,omitempty"`
	Details []string `json:"details,omitempty"`
	Exit    int      `json:"-"`
}

// Describe renders any error in the Translator's language.
func (t *Translator) Describe(err error) Description {
	var e *Error
	var l Localizer
	switch {
	case errors.As(err, &e):
	case errors.As(err, &l):
		e = l.Localized()
	default:
		e = E("error.unexpected").Because(err)
	}
	d := Description{Code: e.Key, Message: t.T(e.Key, e.Args...), Exit: e.ExitCode}
	if e.HintKey != "" {
		d.Hint = t.T(e.HintKey, e.HintArgs...)
	}
	if d.Exit == 0 {
		d.Exit = 1
	}
	for _, detail := range e.Details {
		d.Details = append(d.Details, t.T(detail.Key, detail.Args...))
	}
	if e.Cause != nil {
		cause := t.causeText(e.Cause)
		d.Message += ": " + cause.Message
		if d.Hint == "" {
			d.Hint = cause.Hint
		}
	}
	return d
}

func (t *Translator) causeText(err error) Description {
	var e *Error
	var l Localizer
	if errors.As(err, &e) || errors.As(err, &l) {
		return t.Describe(err)
	}
	return Description{Message: err.Error()}
}

// NotReady is returned by commands and modules planned for a later stage.
func NotReady(name string, stage int) *Error {
	return E("stub.not_ready", name, stage).Hint("stub.not_ready.hint").Exit(3)
}
