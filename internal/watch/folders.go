package watch

import (
	"os"
	"path/filepath"

	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Folders is what the manifest calls important and where it is on this computer.
type Folders struct {
	// Present are the important folders that exist now.
	Present []string
	// Transcripts is the folder of Claude Code's transcripts, whether it exists
	// or not.
	Transcripts string
	// TranscriptsExist says whether it does.
	TranscriptsExist bool
}

// Important finds the folders whose disappearance means that Claude's data moved
// or was lost: Claude Code's folder, the folders the manifest requires inside it
// and every data folder of the desktop application. Their paths come from the
// manifest, so a new location after a Claude update is a change of one file.
func Important(manifest *manifests.Manifest, env discover.Env) Folders {
	found := discover.Locate(manifest, env)
	result := Folders{Transcripts: discover.TranscriptsFolder(manifest, found)}
	add := func(path string) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			result.Present = append(result.Present, path)
		}
	}
	if found.Home.Exists {
		add(found.Home.Path)
		for _, name := range manifest.Claude.Required {
			add(filepath.Join(found.Home.Path, filepath.FromSlash(name)))
		}
	}
	for _, root := range found.Desktop {
		add(root.Path)
	}
	if found.Home.Exists {
		info, err := os.Stat(result.Transcripts)
		result.TranscriptsExist = err == nil && info.IsDir()
	}
	return dedupe(result)
}

func dedupe(folders Folders) Folders {
	seen := map[string]bool{}
	var unique []string
	for _, path := range folders.Present {
		if !seen[path] {
			seen[path] = true
			unique = append(unique, path)
		}
	}
	folders.Present = unique
	return folders
}
