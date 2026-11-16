package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	claude "miya/internal/agent/claude/native"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	conversation "miya/internal/chat/view"
	"miya/internal/controller"
	"miya/internal/mobile"
	phoneserver "miya/internal/phone/server"
)

func TestClaudeBackgroundShellRemainsVisibleAfterReply(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		t.Run(fmt.Sprintf("mobile=%v", mobile), func(t *testing.T) {
			app, _ := testApp(t)
			c := app.C.(*controller.Controller)
			now := time.Now().UnixMilli()
			c.SetThreads([]model.Thread{{ID: 1, Agent: "claude-code", ServerID: "shell", Busy: true, HistoryLoaded: true, StartedAtMs: now - 10000, Messages: []model.Message{{ID: 1, Role: "user", Content: "Deploy"}, {ID: 2, Role: "assistant", Content: "Deployment started in the background.", StartedAtMs: now - 10000}}}})
			c.SetActive(1)
			tasks := []claude.BackgroundTask{{ID: "deploy", Kind: "local_bash", Description: "npm run deploy", Status: "running"}, {ID: "verify", Kind: "local_bash", Command: "verify.sh", Status: "running"}}
			c.HandleClaude(1, claude.Frame{Type: "result", KeepingAlive: true, Result: "Deployment started", BackgroundTasks: tasks})
			if c.Current().Busy || len(c.Current().BackgroundTasks) != 2 {
				t.Fatalf("shell work blocked input or disappeared: %+v", c.Current())
			}
			// Windows and phones receive the thread through JSON rather than the native client.
			thread := c.Current()
			data, err := json.Marshal(thread)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &thread); err != nil {
				t.Fatal(err)
			}
			view := conversation.NewConversationView(c)
			view.Mobile = mobile
			width := 744
			if mobile {
				width = 390
			}
			tt := ui.NewTester(func(ctx *ui.Context) { ui.Column(ctx).Fill().Children(func() { view.MessageList(ctx, thread) }) }, width, 600)
			assertWaiting := func(count int, name string) {
				t.Helper()
				tt.Frame()
				text := strings.Join(tt.Texts(), "\n")
				if !strings.Contains(text, fmt.Sprintf("Background tasks running (%d)", count)) || !strings.Contains(text, name) || strings.Contains(text, "Worked for") {
					t.Fatalf("background wait was presented as completion: %s", text)
				}
			}
			assertWaiting(2, "npm run deploy")
			automationSnapshot(t, tt, fmt.Sprintf("claude-shell-waiting-mobile-%v", mobile))
			tasks[0].Status = "completed"
			c.HandleClaude(1, claude.Frame{Type: "system", Subtype: "task_notification", BackgroundWork: true, TaskID: "deploy", Status: json.RawMessage(`"completed"`), BackgroundTasks: tasks})
			thread = c.Current()
			assertWaiting(1, "verify.sh")
			c.HandleClaude(1, claude.Frame{Type: "system", Subtype: "background_tasks_changed", BackgroundTasks: []claude.BackgroundTask{}})
			thread = c.Current()
			tt.Frame()
			text := strings.Join(tt.Texts(), "\n")
			if strings.Contains(text, "Waiting for results") || !strings.Contains(text, "Worked for") {
				t.Fatalf("completed work did not restore completion footer: %s", text)
			}
			c.HandleClaude(1, claude.Frame{Type: "result", KeepingAlive: true, BackgroundTasks: tasks[1:]})
			c.FinishClaude(1, c.Cwd, nil)
			if len(c.Current().BackgroundTasks) != 0 {
				t.Fatal("process exit left stale background work")
			}
		})
	}
}

// Claude Code resumes a response when background work reports back. Both the
// live label and the finished one cover the whole response, not the last
// segment.
func TestClaudeResumedResponseReportsWholeDuration(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	start := time.Now().UnixMilli() - 10*60*1000
	c.SetThreads([]model.Thread{{ID: 1, Agent: "claude-code", ServerID: "resume", Busy: true, HistoryLoaded: true, StartedAtMs: start, Messages: []model.Message{{ID: 1, Role: "user", Content: "Deploy", SentAtMs: start}, {ID: 2, Role: "assistant", Content: "Deployment started in the background.", StartedAtMs: start}}}})
	c.SetActive(1)
	c.HandleClaude(1, claude.Frame{Type: "result", KeepingAlive: true, Result: "Deployment started"})
	c.HandleClaude(1, claude.Frame{Type: "command_lifecycle", CommandState: "started"})
	if label := msgfmt.ThreadWorkingLabel(c.Current()); !strings.HasPrefix(label, "Working for 10m") {
		t.Fatalf("resumed segment restarted the live clock: %q", label)
	}
	c.HandleClaude(1, claude.Frame{Type: "result", KeepingAlive: true, Result: "Deployed"})
	thread := c.Current()
	groups := msgfmt.GroupMessages(thread.Messages)
	if label := msgfmt.TurnElapsedLabel(groups[len(groups)-1].Messages, false); !strings.HasPrefix(label, "Worked for 10m") {
		t.Fatalf("finished response reported only its last segment: %q", label)
	}
}

// A running turn already shows its tool cards in the message list; the
// background line would only repeat them.
func TestClaudeBackgroundShellHiddenWhileTurnRuns(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		t.Run(fmt.Sprintf("mobile=%v", mobile), func(t *testing.T) {
			app, _ := testApp(t)
			c := app.C.(*controller.Controller)
			now := time.Now().UnixMilli()
			thread := model.Thread{ID: 1, Agent: "claude-code", Busy: true, HistoryLoaded: true, StartedAtMs: now - 10000, Messages: []model.Message{{ID: 1, Role: "user", Content: "Deploy"}}, BackgroundTasks: []model.BackgroundTask{{ID: "deploy", Description: "npm run deploy"}}}
			c.SetThreads([]model.Thread{thread})
			c.SetActive(1)
			view := conversation.NewConversationView(c)
			view.Mobile = mobile
			tt := ui.NewTester(func(ctx *ui.Context) { ui.Column(ctx).Fill().Children(func() { view.MessageList(ctx, thread) }) }, 744, 600)
			tt.Frame()
			if text := strings.Join(tt.Texts(), "\n"); strings.Contains(text, "Background tasks running") {
				t.Fatalf("busy turn repeated its tools as background work: %s", text)
			}
		})
	}
}

func TestClaudeBackgroundShellPhoneSyncWithoutNewMessages(t *testing.T) {
	thread := longThread(2)
	thread.Agent = "claude-code"
	phone := mobile.NewMobileApp()
	sync := phoneserver.NewThreadSync(nil)
	initial, _ := sync.Update(thread)
	phoneOf(t, phone, initial)
	for _, tasks := range [][]model.BackgroundTask{{{ID: "deploy", Description: "npm run deploy"}}, nil} {
		thread.BackgroundTasks = tasks
		delta, changed := sync.Update(thread)
		if !changed || len(delta.Changed) != 0 {
			t.Fatal("background state did not sync independently of message changes")
		}
		phoneOf(t, phone, delta)
		got := phone.Viewer.Current()
		if len(got.BackgroundTasks) != len(tasks) || !sameMessages(got.Messages, thread.Messages) {
			t.Fatalf("phone lost background state or message history: %+v", got)
		}
	}
}
