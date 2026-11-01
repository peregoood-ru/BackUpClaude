package schedule

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
)

// monday is a fixed "now" for the definitions.
var monday = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func plannedTasks() []Task {
	cfg := config.Default()
	return Plan(&cfg, `D:\Program Files\Бэкап\backupclaude.exe`)
}

func taskNamed(t *testing.T, name string) Task {
	t.Helper()
	for _, task := range plannedTasks() {
		if task.Name == name {
			return task
		}
	}
	t.Fatalf("no task %s", name)
	return Task{}
}

func TestPlanFollowsTheSettings(t *testing.T) {
	cfg := config.Default()
	cfg.Schedule = config.Schedule{ClaudeEveryHours: 2, ProjectsEveryHours: 48, CleanupEveryHours: 24, CheckEveryHours: 336}
	byName := map[string]Task{}
	for _, task := range Plan(&cfg, "exe") {
		byName[task.Name] = task
	}
	want := map[string]time.Duration{JobClaude: 2 * time.Hour, JobProjects: 48 * time.Hour, JobCleanup: 24 * time.Hour, JobCheck: 336 * time.Hour}
	for name, every := range want {
		if byName[name].Every != every {
			t.Errorf("%s every %s, want %s", name, byName[name].Every, every)
		}
	}
	if len(byName) != len(JobNames()) {
		t.Errorf("tasks: %v", byName)
	}
}

func TestDefaultPlan(t *testing.T) {
	cases := map[string]time.Duration{JobClaude: time.Hour, JobProjects: 24 * time.Hour, JobCleanup: 168 * time.Hour, JobCheck: 168 * time.Hour}
	for name, every := range cases {
		task := taskNamed(t, name)
		if task.Every != every || !slices.Equal(task.Args, []string{"run", "--job", name}) {
			t.Errorf("%s: %+v", name, task)
		}
	}
}

func TestDefinitionPromisesWhatTheTaskNeeds(t *testing.T) {
	definition, err := buildXML(taskNamed(t, JobClaude), "S-1-5-21-1-2-3-1001", monday)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<StartWhenAvailable>true</StartWhenAvailable>",                // a missed run starts after power on
		"<Hidden>true</Hidden>",                                        // no console window
		"<LogonType>InteractiveToken</LogonType>",                      // the user's session, no stored password
		"<RunLevel>LeastPrivilege</RunLevel>",                          // no administrator rights
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>", // never two runs of one task
		"<Priority>7</Priority>",                                       // below normal
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		"<UserId>S-1-5-21-1-2-3-1001</UserId>",
		"<Interval>PT1H</Interval>",
		"<StartBoundary>2026-11-02T00:07:00</StartBoundary>",
		"<ExecutionTimeLimit>PT2H</ExecutionTimeLimit>",
		`<Arguments>run --job claude</Arguments>`,
		`<Command>D:\Program Files\Бэкап\backupclaude.exe</Command>`,
	} {
		if !strings.Contains(definition, want) {
			t.Errorf("the definition lacks %s", want)
		}
	}
	for _, banned := range []string{"<Password>", "HighestAvailable", "S4U", "Password</LogonType>"} {
		if strings.Contains(definition, banned) {
			t.Errorf("the definition has %s", banned)
		}
	}
}

func TestTriggersByInterval(t *testing.T) {
	cases := []struct {
		job   string
		every time.Duration
		want  string
	}{
		{JobClaude, 3 * time.Hour, "<Interval>PT3H</Interval>"},
		{JobClaude, 36 * time.Hour, "<Interval>P1DT12H</Interval>"},
		{JobProjects, 24 * time.Hour, "<DaysInterval>1</DaysInterval>"},
		{JobProjects, 72 * time.Hour, "<DaysInterval>3</DaysInterval>"},
		{JobCleanup, 168 * time.Hour, "<WeeksInterval>1</WeeksInterval>"},
		{JobCheck, 336 * time.Hour, "<WeeksInterval>2</WeeksInterval>"},
	}
	for _, c := range cases {
		task := taskNamed(t, c.job)
		task.Every = c.every
		definition, err := buildXML(task, "S-1", monday)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, c.want) {
			t.Errorf("%s every %s: no %s in\n%s", c.job, c.every, c.want, definition)
		}
	}
	weekly, _ := buildXML(taskNamed(t, JobCleanup), "S-1", monday)
	if !strings.Contains(weekly, "<Sunday />") {
		t.Error("the weekly task does not name its day")
	}
	task := taskNamed(t, JobClaude)
	task.Every = 30 * time.Minute
	if _, err := buildXML(task, "S-1", monday); err == nil {
		t.Error("an interval under an hour must be refused")
	}
}

func TestDefinitionRoundTrip(t *testing.T) {
	for _, original := range plannedTasks() {
		definition, err := buildXML(original, "S-1", monday)
		if err != nil {
			t.Fatal(err)
		}
		info, err := parseXML(original.Name, []byte(definition))
		if err != nil {
			t.Fatalf("%s: %v", original.Name, err)
		}
		if info.Every != original.Every || info.At != original.At || info.Command != original.Command ||
			!slices.Equal(info.Args, original.Args) || info.TimeLimit != original.TimeLimit {
			t.Errorf("%s: read back %+v from %+v", original.Name, info.Task, original)
		}
		if original.Every%(7*24*time.Hour) == 0 && info.Weekday != original.Weekday {
			t.Errorf("%s: weekday %s", original.Name, info.Weekday)
		}
		if !info.Enabled || !info.StartWhenAvailable || !info.Hidden || info.OnBattery != true || info.Priority != 7 {
			t.Errorf("%s: settings %+v", original.Name, info)
		}
	}
}

func TestArgumentsKeepSpacesAndQuotes(t *testing.T) {
	args := []string{"run", "--job", "a b", `say "hi"`, ""}
	if got := splitArgs(joinArgs(args)); !slices.Equal(got, args) {
		t.Errorf("round trip gave %q", got)
	}
}

func TestDurations(t *testing.T) {
	for in, want := range map[time.Duration]string{time.Hour: "PT1H", 90 * time.Minute: "PT1H30M", 24 * time.Hour: "P1D", 36 * time.Hour: "P1DT12H", 12 * time.Hour: "PT12H"} {
		got := isoDuration(in)
		if got != want || parseISODuration(got) != in {
			t.Errorf("%s: written %s, read back %s", in, got, parseISODuration(got))
		}
	}
}

func TestNamesAreChecked(t *testing.T) {
	for _, bad := range []string{"", "Claude", `..\x`, "a b", "1abc", strings.Repeat("a", 40)} {
		if checkName(bad) == nil {
			t.Errorf("%q passed", bad)
		}
	}
}

// fake stands in for schtasks.exe.
type fake struct {
	calls   [][]string
	files   [][]byte
	answers map[string]fakeAnswer
	userErr error
}

type fakeAnswer struct {
	output string
	err    error
}

func (f *fake) scheduler(t *testing.T) *schtasks {
	return &schtasks{
		workDir: t.TempDir(),
		now:     func() time.Time { return monday },
		userID: func() (string, error) {
			return "S-1-5-21-7", f.userErr
		},
		exec: func(args ...string) ([]byte, error) {
			f.calls = append(f.calls, args)
			if i := slices.Index(args, "/XML"); i >= 0 && args[0] == "/Create" {
				data, err := os.ReadFile(args[i+1])
				if err != nil {
					t.Errorf("the definition is not readable while schtasks runs: %v", err)
				}
				f.files = append(f.files, data)
			}
			answer, ok := f.answers[args[0]+" "+args[len(args)-1]]
			if !ok {
				answer = f.answers[args[0]]
			}
			return []byte(answer.output), answer.err
		},
	}
}

func TestInstallHandsAUtf16DefinitionToSchtasks(t *testing.T) {
	f := &fake{}
	s := f.scheduler(t)
	if err := s.Install(taskNamed(t, JobProjects)); err != nil {
		t.Fatal(err)
	}
	call := f.calls[0]
	if !slices.Equal(call[:3], []string{"/Create", "/TN", `\BackUpClaude\projects`}) || call[len(call)-1] != "/F" {
		t.Errorf("call = %v", call)
	}
	data := f.files[0]
	if data[0] != 0xFF || data[1] != 0xFE {
		t.Fatalf("no UTF-16 byte order mark: % x", data[:4])
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 2; i+1 < len(data); i += 2 {
		units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
	}
	text := string(utf16.Decode(units))
	if !strings.Contains(text, "S-1-5-21-7") || !strings.Contains(text, `Program Files\Бэкап`) || !strings.Contains(text, "<ScheduleByDay>") {
		t.Errorf("definition:\n%s", text)
	}
	if left, _ := os.ReadDir(s.workDir); len(left) != 0 {
		t.Errorf("the definition file stayed behind: %v", left)
	}
}

func TestInstallReportsWhatSchtasksSaid(t *testing.T) {
	f := &fake{answers: map[string]fakeAnswer{"/Create": {"ERROR: Access is denied.", errors.New("exit status 1")}}}
	err := f.scheduler(t).Install(taskNamed(t, JobClaude))
	if err == nil || !strings.Contains(err.Error(), "Access is denied") {
		t.Errorf("err = %v", err)
	}
	f = &fake{userErr: errors.New("no user")}
	if err := f.scheduler(t).Install(taskNamed(t, JobClaude)); err == nil {
		t.Error("an unknown user must stop the install")
	}
	task := taskNamed(t, JobClaude)
	task.Command = ""
	if err := (&fake{}).scheduler(t).Install(task); err == nil {
		t.Error("a task without a program must be refused")
	}
}

func TestRemoveMissingTaskIsNotAnError(t *testing.T) {
	gone := errors.New("exit status 1")
	f := &fake{answers: map[string]fakeAnswer{"/Delete": {"ERROR: cannot find", gone}, "/Query": {"ERROR: cannot find", gone}}}
	if err := f.scheduler(t).Remove(JobClaude); err != nil {
		t.Errorf("err = %v", err)
	}
	refused := &fake{answers: map[string]fakeAnswer{"/Delete": {"ERROR: Access is denied.", gone}, "/Query": {"", nil}}}
	if err := refused.scheduler(t).Remove(JobClaude); err == nil {
		t.Error("a refused delete must be reported")
	}
}

func TestListReadsDefinitionAndRuntime(t *testing.T) {
	definition, err := buildXML(taskNamed(t, JobClaude), "S-1", monday)
	if err != nil {
		t.Fatal(err)
	}
	s := (&fake{}).scheduler(t)
	s.exec = func(args ...string) ([]byte, error) {
		switch {
		case args[len(args)-1] == "/XML" && args[2] == `\BackUpClaude\claude`:
			return []byte(definition), nil
		case slices.Contains(args, "/V") && args[2] == `\BackUpClaude\claude`:
			return []byte(`"PC","\BackUpClaude\claude","02.11.2026 13:07:00","Ready","Interactive only","02.11.2026 12:07:01","0","","C:\x.exe run --job "claude"","N/A"` + "\n"), nil
		}
		return []byte("ERROR: not found"), errors.New("exit status 1")
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != JobClaude {
		t.Fatalf("list = %+v", list)
	}
	got := list[0]
	if got.Status != "Ready" || got.NextRun != "02.11.2026 13:07:00" || got.LastRun != "02.11.2026 12:07:01" || got.LastResult != "0" {
		t.Errorf("runtime fields: %+v", got)
	}
	if !got.StartWhenAvailable || !got.Hidden || got.Every != time.Hour {
		t.Errorf("settings: %+v", got)
	}
}

func TestRunStartsTheNamedTask(t *testing.T) {
	f := &fake{}
	if err := f.scheduler(t).Run(JobCheck); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.calls[0], []string{"/Run", "/TN", `\BackUpClaude\check`}) {
		t.Errorf("call = %v", f.calls[0])
	}
}
