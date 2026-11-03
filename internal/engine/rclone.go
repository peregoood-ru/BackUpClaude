package engine

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// RcloneVersion runs rclone version and returns the version without the
// leading "v".
func RcloneVersion(ctx context.Context, binary string) (string, error) {
	output, err := exec.CommandContext(ctx, binary, "version").Output()
	if err != nil {
		return "", i18n.E("engine.rclone.failed", binary).Because(err)
	}
	version, ok := parseRcloneVersion(string(output))
	if !ok {
		return "", i18n.E("engine.rclone.bad_output")
	}
	return version, nil
}

func parseRcloneVersion(output string) (string, bool) {
	first, _, _ := strings.Cut(output, "\n")
	fields := strings.Fields(first)
	if len(fields) < 2 || fields[0] != "rclone" {
		return "", false
	}
	return strings.TrimPrefix(fields[1], "v"), true
}

// ListRemotes returns the names of the remotes in an rclone settings file,
// without the colon. A missing file has none.
func ListRemotes(ctx context.Context, binary, configFile string) ([]string, error) {
	if _, err := os.Stat(configFile); err != nil {
		return nil, nil
	}
	output, err := exec.CommandContext(ctx, binary, "listremotes", "--config", configFile).Output()
	if err != nil {
		return nil, i18n.E("engine.rclone.failed", binary).Because(err)
	}
	var remotes []string
	for _, line := range strings.Split(string(output), "\n") {
		if name := strings.TrimSuffix(strings.TrimSpace(line), ":"); name != "" {
			remotes = append(remotes, name)
		}
	}
	return remotes, nil
}
