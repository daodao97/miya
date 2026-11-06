package integration

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/widgets"
)

// MIYA_SNAPSHOT=dir go test ./tests/integration -run TestDarkModeSnapshot writes the window in
// dark mode, to look at.
func TestDarkModeSnapshot(t *testing.T) {
	dir := os.Getenv("MIYA_SNAPSHOT")
	if dir == "" {
		t.Skip("set MIYA_SNAPSHOT")
	}
	app, tt := testApp(t)
	t.Cleanup(func() { widgets.ApplyDesktopPalette(false) })
	app.Appearance = "dark"
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "s1", Title: "深色模式", Busy: true, HistoryLoaded: true,
		StartedAtMs: time.Now().Add(-12 * time.Second).UnixMilli(), LastOutputAtMs: time.Now().UnixMilli(),
		Queue: []model.QueuedMessage{{ID: 9, SendRequest: model.SendRequest{Text: "接下来加个测试"}}},
		Messages: []model.Message{
			{ID: 1, Role: "user", Content: "电脑端也增加暗黑模式"},
			{ID: 2, Role: "assistant", Content: "好的，先看配色。\n\n```go\nfunc main() { println(\"hi\") }\n```\n\n- 一项\n- **两项**", Status: "completed"},
			{ID: 3, Role: "tool", Kind: "claudeTool", Content: "Bash\n{\"command\":\"go test ./...\"}", Status: "completed"},
		}}})
	c.SetActive(1)
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
