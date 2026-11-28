package nschat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

func TestMigratedActivityRunsRequireManualExpansion(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Busy: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "inspect"},
		{ID: 2, Role: "tool", Kind: "commandExecution", Status: "inProgress", Item: &model.Activity{Command: "pwd"}, Content: "private output"},
		{ID: 3, Role: "tool", Kind: "commandExecution", Status: "completed", Item: &model.Activity{Command: "ls"}, Content: "second output"},
	}}, nil)
	header := app.Find("id:run-2")
	if !header.Props.Expandable || header.Props.Expanded || !app.Exists("id:activity-run-working") ||
		!strings.Contains(joined(app), "Ran 1 command · 1 running") || app.Exists("id:activity-2") {
		t.Fatal(joined(app))
	}
	c.change(app, func(th *model.Thread) {
		th.Messages[1].Status = "completed"
		th.Messages = append(th.Messages, model.Message{ID: 4, Role: "assistant", Content: "done", Status: "completed"})
		th.Busy = false
	})
	if after := app.Find("id:run-2"); after.ID != header.ID || after.Props.Expanded ||
		app.Exists("id:activity-run-working") || !strings.Contains(joined(app), "Ran 2 commands") ||
		app.Exists("id:activity-2") {
		t.Fatalf("completion replaced the header or expanded activities: %s", joined(app))
	}
	app.Press("id:run-2")
	if !strings.Contains(joined(app), "Ran pwd") {
		t.Fatal(joined(app))
	}
	app.Press("text:Ran pwd")
	if !strings.Contains(joined(app), "private output") {
		t.Fatal(joined(app))
	}
	app.Press("id:run-2")
	if strings.Contains(joined(app), "private output") {
		t.Fatal("collapse retained detail")
	}
	app.Press("id:run-2")
	if !strings.Contains(joined(app), "private output") {
		t.Fatal("reopening the group lost the expanded command")
	}
}

func TestMigratedReasoningHasNoPlaceholderAndExpandsSummary(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Messages: []model.Message{{ID: 1, Role: "user", Content: "why"}, {ID: 2, Role: "reasoning", Kind: "reasoning", Status: "inProgress"}}}, nil)
	if !strings.Contains(joined(app), "Thinking…") || strings.Contains(joined(app), "private") {
		t.Fatal(joined(app))
	}
	c.change(app, func(th *model.Thread) { th.Messages[1].Content = "Checking build flags" })
	app.Press("text:Thinking")
	if !strings.Contains(joined(app), "Checking build flags") {
		t.Fatal(joined(app))
	}
	c.change(app, func(th *model.Thread) {
		th.Messages[1].Content = "Checking build flags and imports"
		th.Messages[1].Status = "completed"
	})
	if !strings.Contains(joined(app), "and imports") {
		t.Fatal("open summary did not update")
	}
}

func TestMigratedCopyAndSelectableMessagesSurviveStreaming(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Busy: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "中文 question", TurnID: "turn"},
		{ID: 2, Role: "assistant", Content: "first paragraph", TurnID: "turn", Status: "inProgress"},
	}}, nil)
	prompt, reply := app.Find("id:prompt-1"), app.Find("id:reply-2")
	if prompt.Props.Selectable != core.SelectableOn || !reply.Props.SelectionArea {
		t.Fatal("message text is not selectable")
	}
	app.SelectText("id:reply-2", core.Bounds{Size: core.Size{Width: 90, Height: 20}})
	c.change(app, func(th *model.Thread) { th.Messages[1].Content += "\n\nmore output" })
	if app.Find("id:reply-2").ID != reply.ID {
		t.Fatal("streaming replaced selected native text")
	}
	c.change(app, func(th *model.Thread) { th.Busy = false; th.Messages[1].Status = "completed" })
	app.Press("label:Copy")
	if app.Clipboard != "first paragraph\n\nmore output" {
		t.Fatalf("copy %q", app.Clipboard)
	}
}

func TestMigratedTurnTimingRateReconnectAndDiff(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Busy: true, StartedAtMs: time.Now().Add(-10 * time.Second).UnixMilli(), TurnOutputTokens: new(400), Messages: []model.Message{
		{ID: 1, Role: "user", Content: "change this", TurnID: "turn"}, {ID: 2, Role: "assistant", Content: "Done", TurnID: "turn", Status: "inProgress"},
	}}, nil)
	if s := joined(app); !strings.Contains(s, "Working for") || !strings.Contains(s, "tokens/s") {
		t.Fatal(s)
	}
	c.change(app, func(th *model.Thread) {
		th.Busy = false
		th.StartedAtMs = 1000
		th.CompletedAtMs = 12500
		th.Messages[1].Status = "completed"
		th.Messages[1].StartedAtMs = 1000
		th.Messages[1].CompletedAtMs = 12500
		th.Messages = append(th.Messages, model.Message{ID: 3, Role: "tool", Kind: "diff", Content: "+one\n+two\n-old", TurnID: "turn"})
	})
	for _, want := range []string{"Worked for 11s", "+2", "−1"} {
		if !strings.Contains(joined(app), want) {
			t.Fatalf("missing %s: %s", want, joined(app))
		}
	}
	app.FastForward(time.Hour)
	if !strings.Contains(joined(app), "Worked for 11s") {
		t.Fatal("fixed completion became wall-clock duration")
	}
	c.change(app, func(th *model.Thread) { th.Messages = th.Messages[:2] })
	if strings.Contains(joined(app), "+2") || strings.Contains(joined(app), "−0") {
		t.Fatal("empty diff retained statistics")
	}
}

func TestMigratedBackgroundActivityStaysAfterReply(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Messages: []model.Message{{ID: 1, Role: "user", Content: "start server"}, {ID: 2, Role: "assistant", Content: "Server started", Status: "completed"}}, BackgroundTasks: []model.BackgroundTask{{Description: "dev server"}}}, nil)
	if !strings.Contains(joined(app), "dev server") {
		t.Fatal(joined(app))
	}
	c.change(app, func(th *model.Thread) { th.BackgroundTasks = nil })
	if strings.Contains(joined(app), "dev server") {
		t.Fatal("finished background task retained")
	}
}

func TestMigratedLongPromptAndActionsFitPhone(t *testing.T) {
	for _, width := range []core.Points{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
				cx.SetTheme(nswidgets.Theme())
				return View{Thread: func() model.Thread {
					return model.Thread{ID: 1, Messages: []model.Message{{ID: 1, Role: "user", Content: strings.Repeat("长消息 long text ", 100)}, {ID: 2, Role: "assistant", Content: "Done", Status: "completed"}}}
				}, OnHandoff: func() {}}
			}, nsgotest.Size(width, 750), nsgotest.Environment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, FontScale: 1.6}))
			for _, selector := range []string{"id:prompt-1", "id:actions-2"} {
				v := app.FindMounted(selector)
				if v.Frame.Size.Width > width {
					t.Fatalf("%s overflows: %+v", selector, v.Frame)
				}
			}
		})
	}
}

func TestMigratedPhonePromptTimestampAndExplicitMemoryOnly(t *testing.T) {
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.Local).UnixMilli()
	app, c := start(t, model.Thread{ID: 1, Messages: []model.Message{{ID: 1, Role: "user", Content: "A phone message", SentAtMs: at}, {ID: 2, Role: "memory", Memory: &model.MemoryNotice{Explicit: false}}}}, nil)
	if !app.Exists("id:timestamp-1") {
		t.Fatal("user timestamp missing without desktop host")
	}
	if app.Exists("label:Edit message") || strings.Contains(joined(app), "Miya memory changed") {
		t.Fatal("phone offered editing or automatic memory notice")
	}
	c.change(app, func(th *model.Thread) { th.Messages[1].Memory.Explicit = true })
	if !strings.Contains(joined(app), "Miya memory changed") {
		t.Fatal("explicit memory notice missing")
	}
}

func TestMigratedWorkingClockAndCompletionKeepStatusPosition(t *testing.T) {
	c := &conversation{thread: model.Thread{ID: 1, Busy: true, StartedAtMs: time.Now().Add(-3 * time.Second).UnixMilli(), Messages: []model.Message{{ID: 1, Role: "user", Content: "question"}, {ID: 2, Role: "assistant", Content: "reply", Status: "inProgress"}}}}
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		c.rev = core.NewSignal(cx, 0)
		draft := core.NewSignal(cx, "")
		return View{Thread: c.get, Draft: draft.Get, OnDraftChange: draft.Set}
	}, nsgotest.Size(390, 750))
	label := func() string {
		for _, s := range app.Texts() {
			if strings.HasPrefix(s, "Working for ") {
				return s
			}
		}
		return ""
	}
	first := label()
	if first == "" {
		t.Fatal("working status missing")
	}
	y := func(v *nsgotest.View) core.Points {
		n := v.Frame.Origin.Y
		for p := v.Parent; p != nil; p = p.Parent {
			n += p.Frame.Origin.Y - p.Offset.Y
		}
		return n
	}
	before := y(app.Find("text:" + first))
	editor := app.Find("id:composer").ID
	app.Advance(2 * time.Second)
	if label() == first {
		t.Fatal("elapsed label froze without incoming model output")
	}
	c.change(app, func(th *model.Thread) {
		th.Busy = false
		th.Messages[1].Status = "completed"
		th.Messages[1].StartedAtMs = 1000
		th.Messages[1].CompletedAtMs = 11000
		th.Messages = append(th.Messages, model.Message{ID: 3, Role: "tool", Kind: "diff", Content: "+new\n-old"})
	})
	after := y(app.Find("text:Worked for 10s"))
	if before != after || app.Find("id:composer").ID != editor {
		t.Fatalf("completion moved status or editor: y %v -> %v", before, after)
	}
}

func TestMigratedRetryKeepsDraftAndRejectsBusyOrStaleErrors(t *testing.T) {
	var got appmodel.RetryRequest
	c := &conversation{thread: model.Thread{ID: 7, HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "user", Content: "question"}, {ID: 2, Role: "error", Content: "upstream unavailable"}}}}
	var draft *core.Signal[string]
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		c.rev = core.NewSignal(cx, 0)
		draft = core.NewSignal(cx, "next draft")
		return View{Thread: c.get, Draft: draft.Get, OnDraftChange: draft.Set, OnRetry: func(r appmodel.RetryRequest) { got = r }}
	}, nsgotest.Size(900, 700))
	app.Press("label:Retry response")
	if got != (appmodel.RetryRequest{ThreadID: 7, MessageID: 2}) || draft.GetUntracked() != "next draft" {
		t.Fatal("retry changed draft or target")
	}
	c.change(app, func(th *model.Thread) { th.Busy = true })
	if app.Enabled("label:Retry response") {
		t.Fatal("retry enabled during busy turn")
	}
	c.change(app, func(th *model.Thread) {
		th.Busy = false
		th.Messages = append(th.Messages, model.Message{ID: 3, Role: "user", Content: "new request"})
	})
	if app.Exists("label:Retry response") {
		t.Fatal("old error retained retry")
	}
}

func TestMigratedStallHintIsVisibleOnlyWhileBusy(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Agent: model.AgentClaude, Busy: true, LastOutputAtMs: time.Now().Add(-90 * time.Second).UnixMilli()}, nil)
	if !strings.Contains(joined(app), "1m 30s") {
		t.Fatal(joined(app))
	}
	c.change(app, func(th *model.Thread) { th.Busy = false })
	if strings.Contains(joined(app), "1m 30s") {
		t.Fatal("idle thread retained stall hint")
	}
}
