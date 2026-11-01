package backupclaude_test

import (
	"testing"

	backupclaude "github.com/peregoood-ru/BackUpClaude"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
)

func TestToolsLockCoversReleasePlatforms(t *testing.T) {
	lock, err := engine.ParseLock(backupclaude.ToolsLock)
	if err != nil {
		t.Fatalf("tools.lock.json: %v", err)
	}
	platforms := []string{"windows/amd64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}
	for _, tool := range []string{engine.ToolRestic, engine.ToolRclone} {
		for _, platform := range platforms {
			if _, _, err := lock.Asset(tool, platform); err != nil {
				t.Errorf("%s on %s: %v", tool, platform, err)
			}
		}
	}
}
