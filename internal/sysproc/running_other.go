//go:build !windows

package sysproc

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// list reads /proc where there is one (Linux) and asks ps elsewhere (macOS).
func list() ([]Process, error) {
	if entries, err := os.ReadDir("/proc"); err == nil {
		var processes []Process
		for _, entry := range entries {
			pid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			path, _ := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
			name := filepath.Base(path)
			if comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm")); err == nil {
				name = strings.TrimSpace(string(comm))
			}
			processes = append(processes, Process{PID: pid, Name: name, Path: path})
		}
		return processes, nil
	}
	output, err := exec.Command("ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	var processes []Process
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		fields := strings.SplitN(strings.TrimSpace(scanner.Text()), " ", 2)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		path := strings.TrimSpace(fields[1])
		processes = append(processes, Process{PID: pid, Name: filepath.Base(path), Path: path})
	}
	return processes, nil
}
