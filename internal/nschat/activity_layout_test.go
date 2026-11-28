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

func TestActivitySummaryAndHeaderlessFileDetailsOnPhone(t *testing.T) {
	thread := model.Thread{ID: 1, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "Edit two files"},
		{ID: 2, Role: "activity", Kind: "fileChange", Status: "completed", Presentation: &model.MessagePresentation{
			Title: "Updated files", Format: "files", Changes: []model.PresentedFileChange{
				{Path: "first.go", Diff: "@@ -1 +1 @@\n-old line\n+new line"},
				{Path: "second.go", Diff: "+another line"},
			},
		}},
		{ID: 3, Role: "tool", Kind: "webSearch", Status: "completed", Agents: []model.SubAgent{{ID: "review", Name: "Review", Status: "running"}}},
		{ID: 4, Role: "assistant", Content: "Checking the result."},
		{ID: 5, Role: "tool", Kind: "commandExecution", Status: "failed", Item: &model.Activity{Command: "go test"}},
	}}
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		return View{Thread: func() model.Thread { return thread }}
	}, nsgotest.Size(320, 750), nsgotest.Environment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, FontScale: 1.6}))
	header := app.Find("id:run-2")
	if header.Frame.Size.Height < 44 || header.Frame.Size.Width > 320 || !app.Exists("id:activity-run-working") {
		t.Fatal("phone summary does not fit or lacks the live activity indicator")
	}
	for _, want := range []string{"Edited 2 files · 1 search · 1 Running", "+2", "−1", "Checking the result.", "Failed · go test"} {
		if !strings.Contains(joined(app), want) {
			t.Fatalf("missing %q in %s", want, joined(app))
		}
	}
	if strings.Contains(joined(app), "first.go") {
		t.Fatal("collapsed activity rendered file details")
	}
	app.Press("id:run-2")
	app.Press("id:activity-2")
	for _, want := range []string{"first.go", "second.go", "-old line", "+new line", "+another line"} {
		if !strings.Contains(joined(app), want) {
			t.Fatalf("missing %q in %s", want, joined(app))
		}
	}
}
