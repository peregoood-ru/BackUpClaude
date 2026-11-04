package sysproc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchesTellsTheAppFromTheCommandLine(t *testing.T) {
	names := []string{"claude.exe"}
	markers := []string{"/WindowsApps/Claude_", "/AnthropicClaude/"}
	for _, item := range []struct {
		process Process
		want    bool
	}{
		{Process{Name: "Claude.exe", Path: `C:\Program Files\WindowsApps\Claude_2.1.0_x64__abc\app\Claude.exe`}, true},
		{Process{Name: "claude.exe", Path: `C:\Users\u\AppData\Local\AnthropicClaude\app-1.0\claude.exe`}, true},
		{Process{Name: "claude.exe", Path: `C:\Users\u\.local\bin\claude.exe`}, false},
		{Process{Name: "claude.exe"}, true}, // the path is unknown: careful answer
		{Process{Name: "notepad.exe", Path: `C:\Windows\notepad.exe`}, false},
	} {
		if got := Matches(item.process, names, markers); got != item.want {
			t.Errorf("%+v: got %v", item.process, got)
		}
	}
	if !Matches(Process{Name: "Claude", Path: "/opt/x/Claude"}, []string{"claude"}, nil) {
		t.Error("without markers the name is enough")
	}
}

func TestFindSeesThisProcess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(self)
	found, err := Find([]string{name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range found {
		if process.PID == os.Getpid() {
			return
		}
	}
	t.Errorf("this test process (%s, %d) is not in %+v", name, os.Getpid(), found)
}
