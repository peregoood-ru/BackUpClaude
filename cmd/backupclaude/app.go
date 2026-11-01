package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	backupclaude "github.com/peregoood-ru/BackUpClaude"
	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Process exit codes.
const (
	exitUsage = 2
)

// errReported marks a failure the command has already shown in its output.
var errReported = errors.New("already reported")

// app is one run of the program.
type app struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	deps           dependencies
	tr             *i18n.Translator
	jsonOut        bool
	flagLang       string
	command        string
}

func newApp(args []string, stdin io.Reader, stdout, stderr io.Writer, deps dependencies) *app {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr, deps: deps}
	explicit, jsonOut := scanGlobalFlags(args)
	a.jsonOut = jsonOut
	if explicit == "" {
		explicit = configuredLanguage()
	}
	a.tr = i18n.New(i18n.Detect(explicit))
	return a
}

// scanGlobalFlags reads --lang and --json before Cobra parses anything: help
// texts and flag errors already need them.
func scanGlobalFlags(args []string) (lang string, jsonOut bool) {
	for i, arg := range args {
		switch {
		case arg == "--":
			return lang, jsonOut
		case arg == "--json":
			jsonOut = true
		case arg == "--lang" && i+1 < len(args):
			lang = args[i+1]
		case strings.HasPrefix(arg, "--lang="):
			lang = strings.TrimPrefix(arg, "--lang=")
		}
	}
	return lang, jsonOut
}

// configuredLanguage reads the language from the settings if they load.
func configuredLanguage() string {
	layout, err := config.Resolve()
	if err != nil {
		return ""
	}
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		return ""
	}
	return cfg.Language
}

// envelope is the shape of every --json answer.
type envelope struct {
	OK      bool              `json:"ok"`
	Command string            `json:"command"`
	Data    any               `json:"data,omitempty"`
	Error   *i18n.Description `json:"error,omitempty"`
}

func (a *app) writeJSON(value envelope) error {
	encoder := json.NewEncoder(a.stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// emit prints a successful result as JSON or as text for a person.
func (a *app) emit(data any, human func(w io.Writer)) error {
	if a.jsonOut {
		return a.writeJSON(envelope{OK: true, Command: a.command, Data: data})
	}
	human(a.stdout)
	return nil
}

// finish reports err and returns the process exit code.
func (a *app) finish(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, errReported) {
		return 1
	}
	description := a.tr.Describe(err)
	if a.jsonOut {
		if writeErr := a.writeJSON(envelope{OK: false, Command: a.command, Error: &description}); writeErr != nil {
			fmt.Fprintln(a.stderr, writeErr)
		}
		return description.Exit
	}
	fmt.Fprintln(a.stderr, a.tr.T("cli.error_prefix"), description.Message)
	for _, detail := range description.Details {
		fmt.Fprintln(a.stderr, "  -", detail)
	}
	if description.Hint != "" {
		fmt.Fprintln(a.stderr, a.tr.T("cli.hint_prefix"), description.Hint)
	}
	return description.Exit
}

// versionInfo is the answer of --version.
type versionInfo struct {
	Version string            `json:"version"`
	Commit  string            `json:"commit"`
	Date    string            `json:"date"`
	Go      string            `json:"go"`
	OS      string            `json:"os"`
	Arch    string            `json:"arch"`
	Tools   map[string]string `json:"tools"`
}

func currentVersion() versionInfo {
	info := versionInfo{
		Version: version, Commit: commit, Date: date,
		Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		Tools: map[string]string{},
	}
	if info.Commit == "none" {
		if build, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range build.Settings {
				if setting.Key == "vcs.revision" {
					info.Commit = setting.Value
				}
			}
		}
	}
	if lock, err := engine.ParseLock(backupclaude.ToolsLock); err == nil {
		for name, tool := range lock.Tools {
			info.Tools[name] = tool.Version
		}
	}
	return info
}

func (a *app) printVersion() error {
	info := currentVersion()
	return a.emit(info, func(w io.Writer) {
		fmt.Fprintln(w, a.tr.T("version.line", info.Version, info.Commit, info.Date, info.Go, info.OS, info.Arch))
		fmt.Fprintln(w, a.tr.T("version.tools", info.Tools[engine.ToolRestic], info.Tools[engine.ToolRclone]))
	})
}

// commandSummary is one line of the --json command list.
type commandSummary struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

func (a *app) listCommands(cmd *cobra.Command) error {
	var commands []commandSummary
	for _, child := range cmd.Commands() {
		if child.IsAvailableCommand() {
			commands = append(commands, commandSummary{child.Name(), child.Short})
		}
	}
	return a.writeJSON(envelope{OK: true, Command: a.command, Data: map[string]any{"commands": commands}})
}
