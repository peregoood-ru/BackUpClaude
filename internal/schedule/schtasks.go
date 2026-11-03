package schedule

import (
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// schtasks drives the Task Scheduler through schtasks.exe, which every Windows
// has and which creates tasks of the current user without administrator rights.
// The calls go through exec so that tests can stand in for the program.
type schtasks struct {
	// exec runs schtasks with the arguments and returns what it printed.
	exec func(args ...string) ([]byte, error)
	// userID gives the identifier of the user the tasks run as.
	userID func() (string, error)
	// workDir holds the task definition for the moment schtasks reads it. It is
	// not under %TEMP%: a program started from a Store package sees a different
	// %TEMP% than the one the scheduler's tools use.
	workDir string
	now     func() time.Time
	// quietHost is a program that runs a console program without a window:
	// conhost.exe --headless. The scheduler shows the console window of a console
	// program even for a hidden task, and the window is on the screen for the
	// whole run. Empty means no such program: the task then runs as it is.
	quietHost string
}

const headlessFlag = "--headless"

// hidden wraps a task so that its console window never appears.
func (s *schtasks) hidden(task Task) Task {
	if s.quietHost == "" {
		return task
	}
	args := append([]string{headlessFlag, task.Command}, task.Args...)
	task.Command, task.Args = s.quietHost, args
	return task
}

// shown is the reverse of hidden, so that a task read back looks as planned.
func (s *schtasks) shown(info *Info) {
	if s.quietHost == "" || !strings.EqualFold(info.Command, s.quietHost) || len(info.Args) < 2 || info.Args[0] != headlessFlag {
		return
	}
	info.Command, info.Args, info.Headless = info.Args[1], info.Args[2:], true
}

func (s *schtasks) fail(action, name string, err error, output []byte) error {
	text := strings.TrimSpace(string(output))
	if text != "" {
		err = errors.New(text)
	}
	return i18n.E("schedule.tool_failed", action, name).Hint("schedule.tool_failed.hint").Because(err)
}

func (s *schtasks) Install(task Task) error {
	if task.Command == "" {
		return i18n.E("schedule.no_command", task.Name)
	}
	user, err := s.userID()
	if err != nil {
		return i18n.E("schedule.no_user").Hint("schedule.no_user.hint").Because(err)
	}
	definition, err := buildXML(s.hidden(task), user, s.now())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.workDir, 0o700); err != nil {
		return i18n.E("schedule.work_failed", s.workDir).Because(err)
	}
	file, err := os.CreateTemp(s.workDir, "task-*.xml")
	if err != nil {
		return i18n.E("schedule.work_failed", s.workDir).Because(err)
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(utf16File(definition)); err != nil {
		_ = file.Close()
		return i18n.E("schedule.work_failed", s.workDir).Because(err)
	}
	if err := file.Close(); err != nil {
		return i18n.E("schedule.work_failed", s.workDir).Because(err)
	}
	output, err := s.exec("/Create", "/TN", taskPath(task.Name), "/XML", filepath.Clean(file.Name()), "/F")
	if err != nil {
		return s.fail("create", task.Name, err, output)
	}
	return nil
}

func (s *schtasks) Remove(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	output, err := s.exec("/Delete", "/TN", taskPath(name), "/F")
	if err == nil {
		return nil
	}
	// schtasks says the same for a missing task and for a refused one, in the
	// language of the OS. Ask whether the task is still there.
	if _, queryErr := s.exec("/Query", "/TN", taskPath(name)); queryErr != nil {
		return nil
	}
	return s.fail("delete", name, err, output)
}

func (s *schtasks) Run(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	output, err := s.exec("/Run", "/TN", taskPath(name))
	if err != nil {
		return s.fail("run", name, err, output)
	}
	return nil
}

func (s *schtasks) List() ([]Info, error) {
	var found []Info
	for _, name := range JobNames() {
		definition, err := s.exec("/Query", "/TN", taskPath(name), "/XML")
		if err != nil {
			continue // not installed
		}
		info, err := parseXML(name, definition)
		if err != nil {
			return nil, s.fail("query", name, err, nil)
		}
		s.shown(&info)
		if runtime, err := s.exec("/Query", "/TN", taskPath(name), "/FO", "CSV", "/NH", "/V"); err == nil {
			info.fillRuntime(runtime)
		}
		found = append(found, info)
	}
	return found, nil
}

// Columns of schtasks /V in CSV form. The column names change with the language
// of the OS, their order does not.
const (
	columnNextRun    = 2
	columnStatus     = 3
	columnLastRun    = 5
	columnLastResult = 6
)

func (i *Info) fillRuntime(output []byte) {
	reader := csv.NewReader(strings.NewReader(strings.ToValidUTF8(string(output), "?")))
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	record, err := reader.Read()
	if err != nil || len(record) <= columnLastResult {
		return
	}
	i.NextRun, i.Status, i.LastRun, i.LastResult = record[columnNextRun], record[columnStatus], record[columnLastRun], record[columnLastResult]
}
