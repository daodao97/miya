package codex

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

const (
	stderrTailBytes = 16 << 10
	stderrTailLines = 4
	stderrLineBytes = 240
)

// Codex can print config values and credentials on stderr. A line that
// mentions one is withheld whole; this is a best-effort filter, so only a few
// short trailing lines are ever shown, and only when the server fails.
var sensitiveStderr = regexp.MustCompile(`(?i)token|bearer|authorization|api[_-]?key|secret|password|cookie|sk-[a-z0-9]`)
var terminalEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// stderrTail keeps the end of the app-server's stderr so a failed start or a
// lost connection can say why.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - stderrTailBytes; extra > 0 {
		t.buf = append(t.buf[:0], t.buf[extra:]...)
	}
	return len(p), nil
}

func (t *stderrTail) Summary() string {
	t.mu.Lock()
	text := string(t.buf)
	t.mu.Unlock()
	var lines []string
	for line := range strings.SplitSeq(terminalEscape.ReplaceAllString(text, ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if sensitiveStderr.MatchString(line) {
			line = "[line withheld]"
		} else if len(line) > stderrLineBytes {
			line = strings.ToValidUTF8(line[:stderrLineBytes], "") + "…"
		}
		lines = append(lines, line)
	}
	if len(lines) > stderrTailLines {
		lines = lines[len(lines)-stderrTailLines:]
	}
	return strings.Join(lines, " · ")
}

const stderrMarker = " (Codex stderr: "

func withStderr(err error, t *stderrTail) error {
	if err == nil || t == nil || strings.Contains(err.Error(), stderrMarker) {
		return err
	}
	summary := t.Summary()
	if summary == "" {
		return err
	}
	return fmt.Errorf("%w%s%s)", err, stderrMarker, summary)
}
