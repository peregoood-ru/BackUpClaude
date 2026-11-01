package schedule

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// folder holds all tasks of the application in the Task Scheduler, so that they
// are easy to find, list and remove.
const folder = "BackUpClaude"

// taskPath is the full name of a task in the Task Scheduler.
func taskPath(name string) string { return `\` + folder + `\` + name }

const (
	layoutBoundary = "2006-01-02T15:04:05"
	// belowNormal is the scheduler's priority 7: the process class below normal.
	belowNormal = 7
)

// buildXML renders the definition of a task. Every choice here has a reason:
//
//   - InteractiveToken with the least privileges: the task runs as the user, in
//     the user's session, without a stored password and without administrator
//     rights. The key store holds the repository password, and only the user's
//     own session can open it.
//   - StartWhenAvailable: a run missed while the computer was off starts as
//     soon as it is on again.
//   - Hidden: the scheduler starts a hidden task in a pseudo-console, so no
//     console window flashes on the screen.
//   - IgnoreNew: a run that is still going is not started a second time.
//   - Battery settings off: a laptop on battery is backed up too. The run has
//     a low priority anyway.
func buildXML(task Task, userID string, now time.Time) (string, error) {
	if err := checkName(task.Name); err != nil {
		return "", err
	}
	trigger, err := triggerXML(task, now)
	if err != nil {
		return "", err
	}
	limit := task.TimeLimit
	if limit <= 0 {
		limit = 12 * time.Hour
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\n")
	b.WriteString(`<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	fmt.Fprintf(&b, "  <RegistrationInfo>\n    <Description>BackUpClaude: %s</Description>\n  </RegistrationInfo>\n", esc(task.Name))
	fmt.Fprintf(&b, "  <Triggers>\n%s  </Triggers>\n", trigger)
	fmt.Fprintf(&b, "  <Principals>\n    <Principal id=\"Author\">\n      <UserId>%s</UserId>\n      <LogonType>InteractiveToken</LogonType>\n      <RunLevel>LeastPrivilege</RunLevel>\n    </Principal>\n  </Principals>\n", esc(userID))
	b.WriteString("  <Settings>\n")
	b.WriteString("    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\n")
	b.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\n")
	b.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\n")
	b.WriteString("    <AllowHardTerminate>true</AllowHardTerminate>\n")
	b.WriteString("    <StartWhenAvailable>true</StartWhenAvailable>\n")
	b.WriteString("    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>\n")
	b.WriteString("    <IdleSettings>\n      <StopOnIdleEnd>false</StopOnIdleEnd>\n      <RestartOnIdle>false</RestartOnIdle>\n    </IdleSettings>\n")
	b.WriteString("    <AllowStartOnDemand>true</AllowStartOnDemand>\n")
	b.WriteString("    <Enabled>true</Enabled>\n")
	b.WriteString("    <Hidden>true</Hidden>\n")
	b.WriteString("    <RunOnlyIfIdle>false</RunOnlyIfIdle>\n")
	b.WriteString("    <WakeToRun>false</WakeToRun>\n")
	fmt.Fprintf(&b, "    <ExecutionTimeLimit>%s</ExecutionTimeLimit>\n", isoDuration(limit))
	fmt.Fprintf(&b, "    <Priority>%d</Priority>\n", belowNormal)
	b.WriteString("  </Settings>\n")
	fmt.Fprintf(&b, "  <Actions Context=\"Author\">\n    <Exec>\n      <Command>%s</Command>\n      <Arguments>%s</Arguments>\n    </Exec>\n  </Actions>\n", esc(task.Command), esc(joinArgs(task.Args)))
	b.WriteString("</Task>\n")
	return b.String(), nil
}

func triggerXML(task Task, now time.Time) (string, error) {
	const day = 24 * time.Hour
	const week = 7 * day
	if task.Every < time.Hour {
		return "", fmt.Errorf("schedule: interval %s is below one hour", task.Every)
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(task.At)
	boundary := start.Format(layoutBoundary)
	switch {
	case task.Every%week == 0:
		return fmt.Sprintf("    <CalendarTrigger>\n      <StartBoundary>%s</StartBoundary>\n      <Enabled>true</Enabled>\n      <ScheduleByWeek>\n        <WeeksInterval>%d</WeeksInterval>\n        <DaysOfWeek>\n          <%s />\n        </DaysOfWeek>\n      </ScheduleByWeek>\n    </CalendarTrigger>\n",
			boundary, task.Every/week, task.Weekday), nil
	case task.Every%day == 0:
		return fmt.Sprintf("    <CalendarTrigger>\n      <StartBoundary>%s</StartBoundary>\n      <Enabled>true</Enabled>\n      <ScheduleByDay>\n        <DaysInterval>%d</DaysInterval>\n      </ScheduleByDay>\n    </CalendarTrigger>\n",
			boundary, task.Every/day), nil
	}
	return fmt.Sprintf("    <TimeTrigger>\n      <Repetition>\n        <Interval>%s</Interval>\n      </Repetition>\n      <StartBoundary>%s</StartBoundary>\n      <Enabled>true</Enabled>\n    </TimeTrigger>\n",
		isoDuration(task.Every), boundary), nil
}

// isoDuration writes d in the form the Task Scheduler reads: P1DT12H.
func isoDuration(d time.Duration) string {
	total := int(d / time.Second)
	days, rest := total/86400, total%86400
	hours, rest := rest/3600, rest%3600
	minutes, seconds := rest/60, rest%60
	var b strings.Builder
	b.WriteString("P")
	if days > 0 {
		fmt.Fprintf(&b, "%dD", days)
	}
	if hours+minutes+seconds > 0 || days == 0 {
		b.WriteString("T")
		if hours > 0 {
			fmt.Fprintf(&b, "%dH", hours)
		}
		if minutes > 0 {
			fmt.Fprintf(&b, "%dM", minutes)
		}
		if seconds > 0 || hours+minutes == 0 {
			fmt.Fprintf(&b, "%dS", seconds)
		}
	}
	return b.String()
}

var durationPattern = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func parseISODuration(value string) time.Duration {
	match := durationPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return 0
	}
	unit := []time.Duration{24 * time.Hour, time.Hour, time.Minute, time.Second}
	var total time.Duration
	for i, part := range match[1:] {
		if n, err := strconv.Atoi(part); err == nil {
			total += time.Duration(n) * unit[i]
		}
	}
	return total
}

func esc(text string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(text))
	return b.String()
}

// joinArgs builds a command line the way Windows programs read it.
func joinArgs(args []string) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\"") {
			arg = `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
		}
		parts[i] = arg
	}
	return strings.Join(parts, " ")
}

// splitArgs is the reverse of joinArgs for what joinArgs writes.
func splitArgs(line string) []string {
	var args []string
	var current strings.Builder
	quoted, started := false, false
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; {
		case r == '\\' && i+1 < len(runes) && runes[i+1] == '"':
			current.WriteRune('"')
			started = true
			i++
		case r == '"':
			quoted, started = !quoted, true
		case (r == ' ' || r == '\t') && !quoted:
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if started {
		args = append(args, current.String())
	}
	return args
}

// utf16File encodes text the way schtasks /XML wants a file: UTF-16 with a
// byte order mark.
func utf16File(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2+2*len(units))
	out = append(out, 0xFF, 0xFE)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

type xmlTask struct {
	Triggers struct {
		Time []struct {
			Interval string `xml:"Repetition>Interval"`
			Start    string `xml:"StartBoundary"`
		} `xml:"TimeTrigger"`
		Calendar []struct {
			Start string `xml:"StartBoundary"`
			Days  struct {
				Interval int `xml:"DaysInterval"`
			} `xml:"ScheduleByDay"`
			Weeks struct {
				Interval int `xml:"WeeksInterval"`
				Days     struct {
					Day []struct {
						XMLName xml.Name
					} `xml:",any"`
				} `xml:"DaysOfWeek"`
			} `xml:"ScheduleByWeek"`
		} `xml:"CalendarTrigger"`
	} `xml:"Triggers"`
	Settings struct {
		DisallowOnBattery  *bool  `xml:"DisallowStartIfOnBatteries"`
		StartWhenAvailable bool   `xml:"StartWhenAvailable"`
		Enabled            *bool  `xml:"Enabled"`
		Hidden             bool   `xml:"Hidden"`
		Priority           *int   `xml:"Priority"`
		TimeLimit          string `xml:"ExecutionTimeLimit"`
	} `xml:"Settings"`
	Actions struct {
		Exec struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

var weekdays = map[string]time.Weekday{
	"Sunday": time.Sunday, "Monday": time.Monday, "Tuesday": time.Tuesday, "Wednesday": time.Wednesday,
	"Thursday": time.Thursday, "Friday": time.Friday, "Saturday": time.Saturday,
}

// parseXML reads a task definition back. schtasks prints it in the console code
// page under a declaration of UTF-16, so the text is taken as it is; bytes that
// are not UTF-8 (a path with national letters) become a question mark.
func parseXML(name string, data []byte) (Info, error) {
	data = []byte(strings.ToValidUTF8(string(data), "?"))
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	var parsed xmlTask
	if err := decoder.Decode(&parsed); err != nil {
		return Info{}, err
	}
	info := Info{Task: Task{Name: name}}
	info.Command = strings.Trim(strings.TrimSpace(parsed.Actions.Exec.Command), `"`)
	info.Args = splitArgs(parsed.Actions.Exec.Arguments)
	info.TimeLimit = parseISODuration(parsed.Settings.TimeLimit)
	info.Enabled = parsed.Settings.Enabled == nil || *parsed.Settings.Enabled
	info.StartWhenAvailable = parsed.Settings.StartWhenAvailable
	info.Hidden = parsed.Settings.Hidden
	info.OnBattery = parsed.Settings.DisallowOnBattery != nil && !*parsed.Settings.DisallowOnBattery
	info.Priority = 7
	if parsed.Settings.Priority != nil {
		info.Priority = *parsed.Settings.Priority
	}
	const day = 24 * time.Hour
	switch {
	case len(parsed.Triggers.Time) > 0:
		trigger := parsed.Triggers.Time[0]
		info.Every = parseISODuration(trigger.Interval)
		info.At = timeOfDay(trigger.Start)
	case len(parsed.Triggers.Calendar) > 0:
		trigger := parsed.Triggers.Calendar[0]
		info.At = timeOfDay(trigger.Start)
		switch {
		case trigger.Weeks.Interval > 0:
			info.Every = time.Duration(trigger.Weeks.Interval) * 7 * day
			if len(trigger.Weeks.Days.Day) > 0 {
				info.Weekday = weekdays[trigger.Weeks.Days.Day[0].XMLName.Local]
			}
		case trigger.Days.Interval > 0:
			info.Every = time.Duration(trigger.Days.Interval) * day
		}
	}
	return info, nil
}

func timeOfDay(boundary string) time.Duration {
	parsed, err := time.Parse(layoutBoundary, strings.TrimSpace(boundary))
	if err != nil {
		return 0
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute + time.Duration(parsed.Second())*time.Second
}
