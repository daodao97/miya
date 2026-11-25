package view

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
)

func TestDelegationCardsInConversation(t *testing.T) {
	const child = "miya://0123456789abcdef0123456789abcdef/thread/claude-code/child"
	notice := fmt.Sprintf("%s Read each fixed result with read_task.\n- Review README: completed (task_id: task-0123456789abcdef0123456789abcdef, session: %s)\n- Check links: failed (task_id: task-1123456789abcdef0123456789abcdef, session: )\n",
		model.DelegationNoticeHeading, child)
	for _, width := range []int{320, 744} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			host := &snapshotHost{thread: model.Thread{ID: 1, Messages: []model.Message{
				{ID: 1, Role: "tool", Kind: "commandExecution", Status: "completed", Item: &model.Activity{Command: "pwd"}},
				{ID: 2, Role: "tool", Kind: "claudeTool", Content: "mcp__miya__start_task\n{\"task\":\"Review the README carefully.\",\"title\":\"Review README\",\"agent\":\"claude\"}", Status: "completed", Delegations: []model.DelegationCard{
					{TaskID: "task-0123456789abcdef0123456789abcdef", Title: "Review README", Agent: "Claude Code", State: "completed", Request: "Review the README carefully.", Quote: "All links resolve."},
				}},
				{ID: 3, Role: "user", Content: notice, Delegations: []model.DelegationCard{
					{TaskID: "task-0123456789abcdef0123456789abcdef", Title: "Review README", State: "completed", Quote: "All links resolve.", Thread: child, Read: true},
					{Title: "Check links", State: "failed"},
				}},
				{ID: 4, Role: "assistant", Content: "I checked the result."},
			}}}
			v := NewConversationView(host)
			v.Mobile = width == 320
			v.Bubbles = v.Mobile
			v.UserGroup = func(*ui.Context, msgfmt.MessageGroup) {
				t.Error("automation receipt reached the editable user bubble")
			}
			var opened string
			v.Md.OnLink = func(ref string) { opened = ref }
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					for _, g := range msgfmt.GroupMessages(host.thread.Messages) {
						v.GroupView(c.Key(g.ID), g)
					}
				})
			}, width, 580)
			for _, copy := range []string{"Delegated to Claude Code", "Delegation results", "All links resolve.", "Check links", "I checked the result."} {
				if !tt.HasText(copy) {
					t.Errorf("missing %q in %v", copy, tt.Texts())
				}
			}
			if tt.HasText("read_task") || tt.HasText("task-0123456789abcdef0123456789abcdef") || tt.HasText("Review the README carefully.") {
				t.Fatalf("showed raw prompt, identifiers or collapsed details: %v", tt.Texts())
			}
			if err := tt.Click("Delegated to Claude Code · Review README"); err != nil || !tt.HasText("Review the README carefully.") {
				t.Fatalf("could not expand the delegated call: %v, %v", err, tt.Texts())
			}
			if err := tt.Click("Open target conversation"); err != nil || opened != child {
				t.Fatalf("could not open the child conversation: %v, %q", err, opened)
			}
			if !tt.HasText("Read by the main agent") {
				t.Errorf("receipt did not say the parent read the result: %v", tt.Texts())
			}
			if err := tt.Click("Delegated to Claude Code · Review README"); err != nil || tt.HasText("Review the README carefully.") {
				t.Fatalf("could not collapse the delegated call: %v, %v", err, tt.Texts())
			}

			host.thread.Messages[2].Delegations = nil
			tt.Frame()
			if !tt.HasText("Check links") || tt.HasText("read_task") {
				t.Fatalf("legacy batch receipt could not render: %v", tt.Texts())
			}
		})
	}
}

func TestDelegationReceiptFoldsSettledTasks(t *testing.T) {
	cards := []model.DelegationCard{
		{Title: "One", State: "completed"}, {Title: "Two", State: "completed"}, {Title: "Three", State: "completed"},
		{Title: "Four", State: "completed"}, {Title: "Five", State: "failed", Detail: "Tests failed"},
	}
	notice := model.DelegationNoticeHeading + " Read each fixed result with read_task.\n"
	host := &snapshotHost{thread: model.Thread{ID: 1, Messages: []model.Message{{ID: 1, Role: "user", Content: notice, Delegations: cards}}}}
	v := NewConversationView(host)
	tt := ui.NewTester(func(c *ui.Context) {
		for _, g := range msgfmt.GroupMessages(host.thread.Messages) {
			v.GroupView(c.Key(g.ID), g)
		}
	}, 744, 900)
	if tt.HasText("Four") || !tt.HasText("Five") || !tt.HasText("Tests failed") || !tt.HasText("1 more tasks") {
		t.Fatalf("receipt must fold settled tasks and keep failures: %v", tt.Texts())
	}
	if err := tt.Click("1 more tasks"); err != nil || !tt.HasText("Four") || !tt.HasText("Show fewer tasks") {
		t.Fatalf("could not list every task: %v, %v", err, tt.Texts())
	}
}

func TestSendThreadIsNotADelegatedTask(t *testing.T) {
	m := model.Message{Role: "tool", Kind: "claudeTool", Content: "mcp__miya__send_thread\n{\"thread_id\":\"x\",\"message\":\"hi\"}", Status: "completed"}
	if msgfmt.IsDelegationCall(m) {
		t.Fatal("send_thread has no task to follow")
	}
}

func TestDelegationReceiptRevealsItsCall(t *testing.T) {
	const id = "task-0123456789abcdef0123456789abcdef"
	notice := model.DelegationNoticeHeading + " Read each fixed result with read_task.\n"
	messages := []model.Message{
		{ID: 1, Role: "tool", Kind: "claudeTool", Content: "mcp__miya__start_task\n{\"task\":\"Review the README carefully.\",\"title\":\"Review README\"}", Status: "completed",
			Delegations: []model.DelegationCard{{TaskID: id, Title: "Review README", State: "completed", Request: "Review the README carefully.", Quote: "All links resolve."}}},
	}
	for i := range 12 {
		messages = append(messages, model.Message{ID: 2 + i, Role: "assistant", Content: fmt.Sprintf("Working on step %d.", i), TurnID: fmt.Sprint(i)})
	}
	messages = append(messages, model.Message{ID: 100, Role: "user", Content: notice, Delegations: []model.DelegationCard{{TaskID: id, Title: "Review README", State: "completed", Quote: "All links resolve."}}})
	host := &snapshotHost{thread: model.Thread{ID: 1, Messages: messages}}
	v := NewConversationView(host)
	tt := ui.NewTester(func(c *ui.Context) { v.MessageList(c, host.thread) }, 744, 580)
	if tt.HasText("Review the README carefully.") {
		t.Fatalf("call card started open: %v", tt.Texts())
	}
	if err := tt.Click("Show delegated call · Review README"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Review the README carefully.") {
		t.Fatalf("receipt did not reveal its call card: %v", tt.Texts())
	}
}

func TestDelegationTextRendersMarkdown(t *testing.T) {
	const request = "## Inputs\n- First **file**\n- Second file"
	card := model.DelegationCard{TaskID: "task-0123456789abcdef0123456789abcdef", Title: "Hero", State: "completed", Request: request, Quote: "## Done\nAll **good**."}
	call := model.Message{ID: 1, Role: "tool", Kind: "claudeTool", Content: "mcp__miya__start_task\n{\"title\":\"Hero\"}", Status: "completed", Delegations: []model.DelegationCard{card}}
	notice := model.Message{ID: 2, Role: "user", Content: model.DelegationNoticeHeading + " Read each fixed result with read_task.\n", Delegations: []model.DelegationCard{card}}
	host := &snapshotHost{thread: model.Thread{ID: 1, Messages: []model.Message{call, notice}}}
	v := NewConversationView(host)
	v.Open[1] = true
	tt := ui.NewTester(func(c *ui.Context) {
		for _, g := range msgfmt.GroupMessages(host.thread.Messages) {
			v.GroupView(c.Key(g.ID), g)
		}
	}, 744, 900)
	for _, text := range tt.Texts() {
		if strings.Contains(text, "##") || strings.Contains(text, "**") {
			t.Fatalf("showed Markdown marks: %q in %v", text, tt.Texts())
		}
	}
	if !tt.HasText("Inputs") || !tt.HasText("Second file") {
		t.Fatalf("lost the rendered request: %v", tt.Texts())
	}
}
