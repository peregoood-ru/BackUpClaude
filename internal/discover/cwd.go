package discover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	cwdKey        = "cwd"
	maxCwdLength  = 32 << 10
	cwdReadBuffer = 64 << 10
)

// ScanCwds hands visit each top-level "cwd" value of a JSON Lines transcript
// that differs from the one before it, until visit returns true. It reads a byte
// at a time and keeps nothing but the value: transcripts reach 120 MB and a
// single line can be tens of megabytes, so neither the file nor a line may be
// held in memory. Only a cwd at the top level of a line counts. The same word
// inside a tool's input or a message is ignored.
func ScanCwds(r io.Reader, visit func(cwd string) (stop bool)) error {
	reader := bufio.NewReaderSize(r, cwdReadBuffer)
	var scan cwdScanner
	for {
		b, err := reader.ReadByte()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if value, ok := scan.feed(b); ok && visit(value) {
			return nil
		}
	}
}

// FirstCwd returns the first top-level "cwd" value, or "" when there is none.
func FirstCwd(r io.Reader) (string, error) {
	first := ""
	err := ScanCwds(r, func(cwd string) bool {
		first = cwd
		return true
	})
	return first, err
}

// cwdScanner is a small JSON state machine. It forgets everything at a line
// break, so one damaged line cannot spoil the rest of the file. JSON strings
// never hold a raw line break.
type cwdScanner struct {
	depth      int
	inString   bool
	escaped    bool
	afterColon bool
	isCwdKey   bool
	capturing  bool
	overflow   bool
	buffer     []byte
	// last spares decoding: every line of a transcript repeats the cwd.
	last []byte
}

func (s *cwdScanner) feed(b byte) (string, bool) {
	if b == '\n' {
		*s = cwdScanner{buffer: s.buffer[:0], last: s.last}
		return "", false
	}
	if s.inString {
		return s.feedString(b)
	}
	switch b {
	case '"':
		s.inString = true
		s.capturing = s.depth == 1 && (!s.afterColon || s.isCwdKey)
		s.overflow = false
		s.buffer = s.buffer[:0]
	case '{', '[':
		s.depth++
	case '}', ']':
		s.depth--
	case ':':
		if s.depth == 1 {
			s.afterColon = true
		}
	case ',':
		if s.depth == 1 {
			s.afterColon, s.isCwdKey = false, false
		}
	}
	return "", false
}

func (s *cwdScanner) feedString(b byte) (string, bool) {
	switch {
	case s.escaped:
		s.escaped = false
	case b == '\\':
		s.escaped = true
	case b == '"':
		s.inString = false
		if !s.capturing {
			return "", false
		}
		s.capturing = false
		if !s.afterColon {
			s.isCwdKey = string(s.buffer) == cwdKey
			return "", false
		}
		return s.decoded()
	}
	if s.capturing {
		if len(s.buffer) < maxCwdLength {
			s.buffer = append(s.buffer, b)
		} else {
			s.overflow = true
		}
	}
	return "", false
}

func (s *cwdScanner) decoded() (string, bool) {
	if s.overflow || bytes.Equal(s.buffer, s.last) {
		return "", false
	}
	s.last = append(s.last[:0], s.buffer...)
	quoted := make([]byte, 0, len(s.buffer)+2)
	quoted = append(append(append(quoted, '"'), s.buffer...), '"')
	var value string
	if json.Unmarshal(quoted, &value) != nil || value == "" {
		return "", false
	}
	return value, true
}
