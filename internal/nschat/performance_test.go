package nschat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

func longTranscript(turns int) model.Thread {
	thread := model.Thread{ID: 1, Busy: true, HistoryLoaded: true}
	for i := 0; i < turns; i++ {
		turn := fmt.Sprint(i)
		status := "completed"
		if i == turns-1 {
			status = "inProgress"
		}
		thread.Messages = append(thread.Messages,
			model.Message{ID: i*3 + 1, TurnID: turn, Role: "user", Content: "Check the build and outline the changes"},
			model.Message{ID: i*3 + 2, TurnID: turn, Role: "tool", Kind: "commandExecution", Status: "completed", Content: "Tests passed", Item: &model.Activity{Command: "go test ./..."}},
			model.Message{ID: i*3 + 3, TurnID: turn, Role: "assistant", Status: status, Content: strings.Repeat("The build **passes**. Here is a short description of the change.\n\n", 5)},
		)
	}
	thread.TurnID = fmt.Sprint(turns - 1)
	return thread
}

func BenchmarkLongTranscript(b *testing.B) {
	app, c := startLongTranscript(b)
	if !app.Exists("id:reply-420") {
		b.Fatal("streaming reply is not mounted")
	}
	base := c.thread.Messages[len(c.thread.Messages)-1].Content
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.change(app, func(t *model.Thread) {
			t.Messages[len(t.Messages)-1].Content = base + fmt.Sprint(i)
		})
	}
}

func startLongTranscript(t testing.TB) (*nsgotest.App, *conversation) {
	t.Helper()
	c := &conversation{thread: longTranscript(140)}
	c.thread.Messages[2].Content = "An old reply with a unique search phrase"
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		c.rev = core.NewSignal(cx, 0)
		return View{Thread: c.get, TestID: "long-transcript"}
	}, nsgotest.Size(390, 844), nsgotest.Environment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone}))
	return app, c
}

func TestLongTranscriptMountsNearbyTurnsAndFindsOlderReplies(t *testing.T) {
	app, c := startLongTranscript(t)
	if app.Exists("id:reply-3") || !app.Exists("id:reply-420") {
		t.Fatal("opening long history should mount the latest replies first")
	}
	reply := app.Find("id:reply-420")
	c.change(app, func(th *model.Thread) { th.Messages[419].Content += "\n\nStill streaming" })
	if app.Find("id:reply-420").ID != reply.ID || !app.Exists("text:Still streaming") {
		t.Fatal("visible reply was replaced or failed to update")
	}
	app.ScrollTo("id:chat-transcript", core.Point{})
	if !app.Exists("id:reply-3") || app.Exists("id:reply-420") {
		t.Fatal("scrolling back did not mount older turns")
	}
	bounds := app.Find("id:chat-transcript").Offset
	c.change(app, func(th *model.Thread) { th.Messages[419].Content += " again" })
	if app.Find("id:chat-transcript").Offset != bounds {
		t.Fatal("streaming moved the reader away from the older turns")
	}
	app.Press("id:activity-2")
	if !app.Exists("text:Tests passed") {
		t.Fatal("could not inspect the old tool output")
	}
	app.Press("label:↓ Latest")
	if !app.Exists("id:reply-420") || app.Exists("id:reply-3") {
		t.Fatal("jump to latest did not restore the end of the conversation")
	}
	app.ScrollTo("id:chat-transcript", core.Point{})
	if !app.Exists("text:Tests passed") || !app.Find("id:activity-2").Props.Expanded {
		t.Fatal("recycling a turn lost its expanded tool output")
	}
	app.Key("mod+f")
	app.Type("id:chat-find-input", "unique search phrase")
	if !app.Exists("text:1 / 1") && !app.Exists("text:1 of 1") {
		t.Fatal("full history search did not find the oldest reply")
	}
}
