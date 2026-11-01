package engine

import (
	"context"
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
