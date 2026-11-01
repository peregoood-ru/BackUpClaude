package engine

import (
	"encoding/json"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// BackupSummary is restic's summary message of a backup run.
type BackupSummary struct {
	FilesNew            int64   `json:"files_new"`
	FilesChanged        int64   `json:"files_changed"`
	FilesUnmodified     int64   `json:"files_unmodified"`
	DirsNew             int64   `json:"dirs_new"`
	DirsChanged         int64   `json:"dirs_changed"`
	DirsUnmodified      int64   `json:"dirs_unmodified"`
	DataAdded           int64   `json:"data_added"`
	DataAddedPacked     int64   `json:"data_added_packed"`
	TotalFilesProcessed int64   `json:"total_files_processed"`
	TotalBytesProcessed int64   `json:"total_bytes_processed"`
	TotalDuration       float64 `json:"total_duration"`
	SnapshotID          string  `json:"snapshot_id"`
}

// RestoreSummary is restic's summary message of a restore run.
type RestoreSummary struct {
	SecondsElapsed float64 `json:"seconds_elapsed"`
	TotalFiles     int64   `json:"total_files"`
	FilesRestored  int64   `json:"files_restored"`
	FilesSkipped   int64   `json:"files_skipped"`
	TotalBytes     int64   `json:"total_bytes"`
	BytesRestored  int64   `json:"bytes_restored"`
	BytesSkipped   int64   `json:"bytes_skipped"`
}

// Snapshot is one entry of restic snapshots --json.
type Snapshot struct {
	ID       string           `json:"id"`
	ShortID  string           `json:"short_id"`
	Time     time.Time        `json:"time"`
	Hostname string           `json:"hostname"`
	Paths    []string         `json:"paths"`
	Tags     []string         `json:"tags,omitempty"`
	Summary  *SnapshotSummary `json:"summary,omitempty"`
}

// SnapshotSummary is what restic remembers about the run that made a snapshot.
type SnapshotSummary struct {
	TotalFilesProcessed int64 `json:"total_files_processed"`
	TotalBytesProcessed int64 `json:"total_bytes_processed"`
	DataAdded           int64 `json:"data_added"`
}

// Node is one entry of a snapshot's file tree.
type Node struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"`
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
}

// Node types.
const (
	NodeFile = "file"
	NodeDir  = "dir"
)

// parseNode reads one line of restic ls --json. The first line describes the
// snapshot and has no node fields.
func parseNode(line []byte) (Node, bool) {
	var node Node
	if json.Unmarshal(line, &node) != nil || node.Path == "" || node.Type == "" {
		return Node{}, false
	}
	return node, true
}

// ItemError is a file restic could not process during a backup.
type ItemError struct {
	Item    string `json:"item"`
	During  string `json:"during"`
	Message string `json:"message"`
}

type messageHeader struct {
	MessageType string `json:"message_type"`
}

// parseBackupLine reads one line of restic backup --json. Status messages and
// anything that is not JSON are skipped: restic may add messages at any time.
func parseBackupLine(line []byte) (*BackupSummary, *ItemError) {
	var header messageHeader
	if json.Unmarshal(line, &header) != nil {
		return nil, nil
	}
	switch header.MessageType {
	case "summary":
		var summary BackupSummary
		if json.Unmarshal(line, &summary) != nil {
			return nil, nil
		}
		return &summary, nil
	case "error":
		return nil, parseItemError(line)
	}
	return nil, nil
}

// errorText accepts both forms restic has used: an object with a message and a
// plain string.
func errorText(raw json.RawMessage) string {
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &object) == nil && object.Message != "" {
		return object.Message
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

// parseRestoreLine reads one line of restic restore --json.
func parseRestoreLine(line []byte) (*RestoreSummary, *ItemError) {
	var header messageHeader
	if json.Unmarshal(line, &header) != nil {
		return nil, nil
	}
	switch header.MessageType {
	case "summary":
		var summary RestoreSummary
		if json.Unmarshal(line, &summary) != nil {
			return nil, nil
		}
		return &summary, nil
	case "error":
		return nil, parseItemError(line)
	}
	return nil, nil
}

func parseItemError(line []byte) *ItemError {
	var msg struct {
		Item   string          `json:"item"`
		During string          `json:"during"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return nil
	}
	return &ItemError{Item: msg.Item, During: msg.During, Message: errorText(msg.Error)}
}

func parseSnapshots(data []byte) ([]Snapshot, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var snapshots []Snapshot
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return nil, i18n.E("engine.restic.bad_output").Because(err)
	}
	return snapshots, nil
}
