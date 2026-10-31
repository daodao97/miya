package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// MIYA_SNAPSHOT=dir go test -run TestDarkModeSnapshot writes the window in
// dark mode, to look at.
func TestDarkModeSnapshot(t *testing.T) {
	dir := os.Getenv("MIYA_SNAPSHOT")
	if dir == "" {
		t.Skip("set MIYA_SNAPSHOT")
	}
	app, tt := testApp(t)
	t.Cleanup(func() { applyDesktopPalette(false) })
	app.appearance = "dark"
	c := app.c
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "s1", Title: "深色模式", Busy: true, HistoryLoaded: true,
		StartedAtMs: time.Now().Add(-12 * time.Second).UnixMilli(), LastOutputAtMs: time.Now().UnixMilli(),
		Queue: []queuedMessage{{ID: 9, sendRequest: sendRequest{Text: "接下来加个测试"}}},
		Messages: []Message{
			{ID: 1, Role: "user", Content: "电脑端也增加暗黑模式"},
			{ID: 2, Role: "assistant", Content: "好的，先看配色。\n\n```go\nfunc main() { println(\"hi\") }\n```\n\n- 一项\n- **两项**", Status: "completed"},
			{ID: 3, Role: "tool", Kind: "claudeTool", Content: "Bash\n{\"command\":\"go test ./...\"}", Status: "completed"},
		}}})
	c.setActive(1)
	tt.Frame()
	f, err := os.Create(filepath.Join(dir, "dark.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
