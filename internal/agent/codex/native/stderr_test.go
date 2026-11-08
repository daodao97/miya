package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStderrSummaryWithholdsCredentials(t *testing.T) {
	tail := &stderrTail{}
	tail.Write([]byte("early line\n\x1b[31mERROR\x1b[0m config: unknown key mcp_servers.x\nAuthorization: Bearer abc123\nOPENAI_API_KEY=sk-live\nlast reason\n"))
	got := tail.Summary()
	if strings.Contains(got, "abc123") || strings.Contains(got, "sk-live") {
		t.Fatalf("credential leaked: %q", got)
	}
	if strings.Contains(got, "early line") || !strings.Contains(got, "ERROR config: unknown key") || !strings.Contains(got, "last reason") {
		t.Fatalf("unexpected summary: %q", got)
	}
	for range 2000 {
		tail.Write([]byte("filler line that keeps the tail bounded\n"))
	}
	if len(tail.buf) > stderrTailBytes {
		t.Fatalf("tail grew to %d bytes", len(tail.buf))
	}
}

func TestFailedStartReportsStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	script := filepath.Join(t.TempDir(), "codex")
	body := "#!/bin/sh\necho 'Error: unknown config key mcp_servers.broken' >&2\necho 'token=secret-value' >&2\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := Start(ctx, script, t.TempDir())
	if err == nil {
		t.Fatal("start succeeded")
	}
	if !strings.Contains(err.Error(), "unknown config key mcp_servers.broken") {
		t.Fatalf("stderr reason missing: %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("credential leaked: %v", err)
	}
}
