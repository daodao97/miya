package nschat

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/nswidgets"
)

func TestLongToolCallStaysInsideMessageColumn(t *testing.T) {
	for _, width := range []int{390, 900} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			command := "./scripts/build-macos.sh " + strings.Repeat("--target macos_arm64 ", 20)
			message := model.Message{ID: 2, Role: "commandExecution", Kind: "commandExecution", Status: "completed",
				Item: &model.Activity{Command: command}, Content: "Build and signature verification passed", TurnID: "t1"}
			thread := model.Thread{ID: 1, HistoryLoaded: true, Messages: []model.Message{
				{ID: 1, Role: "user", Content: "Build Miya", TurnID: "t1"}, message,
			}}
			app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
				cx.SetTheme(nswidgets.Theme())
				return View{Thread: func() model.Thread { return thread }}
			}, nsgotest.Size(core.Points(width), 800))
			title := app.Find("text:" + msgfmt.Message(message).Title)
			header := title.Parent
			if header.Frame.Size.Width > maxWidth || header.Frame.Size.Width > core.Points(width) {
				t.Fatalf("tool header wider than message column: %+v", header.Frame)
			}
			for _, child := range header.Children {
				if child.Frame.Origin.X < 0 || child.Frame.Origin.X+child.Frame.Size.Width > header.Frame.Size.Width+0.5 {
					t.Fatalf("tool header child overflows: child=%+v header=%+v", child.Frame, header.Frame)
				}
			}
			if title.MaxLines != 1 || title.Ellipsize != core.EllipsizeTail {
				t.Fatal("long tool title must truncate at the end of one line")
			}
			app.Press("view:" + strconv.Itoa(int(header.ID)))
			app.Settle()
			if !strings.Contains(joined(app), message.Content) {
				t.Fatal("truncated tool title no longer expands to its output")
			}
		})
	}
}
