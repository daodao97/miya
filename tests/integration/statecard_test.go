package integration

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	mcpserver "miya/internal/mcp"
	mcpproto "miya/internal/mcp/protocol"
	svcclient "miya/internal/service/client"
	"miya/internal/statecard"
)

// stateCardHost is a controller that keeps state cards, in a directory of
// the test.
func stateCardHost(t *testing.T) (*controller.Controller, string) {
	t.Helper()
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	dir := filepath.Join(t.TempDir(), "status")
	c.StartStateCards(dir)
	return c, dir
}

// stateCardServer is the MCP server of an agent, reaching c as the service.
func stateCardServer(c *controller.Controller) *mcpserver.Server {
	return mcpserver.NewServer("/ws", func(op string, args, out any) error {
		res, err := c.MCP().Handle(op, args.(mcpproto.Request))
		if err == nil {
			assign(out, res)
		}
		return err
	}, func(time.Duration) {})
}

func statusRequest(args mcpproto.StatusArgs) mcpproto.Request {
	raw, _ := json.Marshal(args)
	return mcpproto.Request{Tool: "update_status", Arguments: raw, Workspace: "/untrusted"}
}

// statusCall is the update_status call an agent reports while it runs.
func statusCall(agent string, r mcpproto.Request, id int, item string) model.Message {
	m := memoryToolMessage(agent, r.Tool, r.Arguments, id, time.Now().UnixMilli())
	m.ItemID = item
	return m
}

func writeStateCard(t *testing.T, c *controller.Controller, r mcpproto.Request) {
	t.Helper()
	if _, err := c.MCP().StateCard(r); err != nil {
		t.Fatal(err)
	}
}

// storedStateCards reads the directory again, as the next start of the
// service does.
func storedStateCards(t *testing.T, dir string) *statecard.Store {
	t.Helper()
	store, err := statecard.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStateCardIsWrittenByItsConversationAndShownToOthers(t *testing.T) {
	for _, agent := range []string{appmodel.AgentCodex, appmodel.AgentClaude} {
		t.Run(agent, func(t *testing.T) {
			c, dir := stateCardHost(t)
			s := stateCardServer(c)
			r := statusRequest(mcpproto.StatusArgs{Purpose: "修复登录过期", Done: []string{"定位到时区换算"}, Remaining: []string{"补测试"}, WaitingFor: "确认是否兼容旧令牌", Checked: []string{"go test ./auth"}, References: []string{"auth/session.go"}})
			c.SetThreads([]model.Thread{{ID: 1, Agent: agent, ServerID: "session-a", Title: "登录", Busy: true, HistoryLoaded: true, Messages: []model.Message{{ID: 10, Role: "user", Content: "修一下登录", SentAtMs: 1000}, statusCall(agent, r, 11, "item-1")}}})
			saved, err := s.CallTool("update_status", r.Arguments)
			if err != nil || !strings.Contains(saved, "State card saved") || !strings.Contains(saved, "Miya has not verified it") {
				t.Fatalf("card not saved: %q %v", saved, err)
			}
			store := storedStateCards(t, dir)
			card, ok := store.Get(statecard.Key(agent, "session-a"))
			if !ok || store.Len() != 1 || card.Purpose != "修复登录过期" || card.WaitingFor != "确认是否兼容旧令牌" || card.Author != statecard.AuthorAgent || card.Stale || card.Turn != "turn" || card.Item != "item-1" || card.WrittenAtMs == 0 {
				t.Fatalf("card not kept as written: %+v %v", card, ok)
			}
			listing, err := s.CallTool("list_threads", json.RawMessage(`{}`))
			if err != nil || !strings.Contains(listing, "State card, not verified by Miya: 修复登录过期 · Remaining: 补测试 · Waiting for the person: 确认是否兼容旧令牌") || strings.Contains(listing, "Outdated") {
				t.Fatalf("card missing from the list: %q %v", listing, err)
			}
			args, _ := json.Marshal(map[string]any{"id": c.ThreadRef(c.ThreadWithID(1)).URL()})
			status, err := s.CallTool("thread_status", args)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"State card, written by the agent at ", "Miya has not verified it.", "Purpose: 修复登录过期", "Done:\n- 定位到时区换算", "Remaining:\n- 补测试", "Waiting for the person: 确认是否兼容旧令牌", "Checked, by the writer's own account:\n- go test ./auth", "References:\n- auth/session.go"} {
				if !strings.Contains(status, want) {
					t.Fatalf("status lacks %q: %q", want, status)
				}
			}
			if strings.Contains(status, "Outdated") {
				t.Fatalf("a fresh card reads as outdated: %q", status)
			}
		})
	}
}

func TestStateCardAmbiguousOrMissingCallerWritesNothing(t *testing.T) {
	c, dir := stateCardHost(t)
	r := statusRequest(mcpproto.StatusArgs{Purpose: "同一件事"})
	call := statusCall(appmodel.AgentCodex, r, 11, "item-1")
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "a", Busy: true, Messages: []model.Message{call}}, {ID: 2, ServerID: "b", Busy: true, Messages: []model.Message{call}}})
	if _, err := c.MCP().StateCard(r); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous caller accepted: %v", err)
	}
	// A call with other arguments is another conversation's.
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "a", Busy: true, Messages: []model.Message{call}}})
	if _, err := c.MCP().StateCard(statusRequest(mcpproto.StatusArgs{Purpose: "另一件事"})); err == nil || !strings.Contains(err.Error(), mcpproto.CallerUnknown) {
		t.Fatalf("a call nobody made was accepted: %v", err)
	}
	c.SetThreads(nil)
	if _, err := c.MCP().StateCard(r); err == nil || !strings.Contains(err.Error(), mcpproto.CallerUnknown) {
		t.Fatalf("missing caller accepted: %v", err)
	}
	if n := storedStateCards(t, dir).Len(); n != 0 {
		t.Fatalf("%d cards written without a caller", n)
	}
}

func TestStateCardIsOutdatedOnceThePersonWritesAgain(t *testing.T) {
	agent := appmodel.AgentClaude
	c, dir := stateCardHost(t)
	r := statusRequest(mcpproto.StatusArgs{Purpose: "修复登录过期", Remaining: []string{"补测试"}})
	c.SetThreads([]model.Thread{{ID: 1, Agent: agent, ServerID: "session-a", Busy: true, Messages: []model.Message{{ID: 10, Role: "user", Content: "修一下登录", SentAtMs: 1000}, statusCall(agent, r, 11, "item-1")}}})
	writeStateCard(t, c, r)
	// The turn ends with the agent's answer: the card still stands.
	c.Update(1, func(th *model.Thread) {
		th.Busy = false
		th.Messages[1].Status = "completed"
		th.Messages = append(th.Messages, model.Message{ID: 12, Role: "assistant", Content: "已定位到时区换算", CompletedAtMs: time.Now().UnixMilli()})
	})
	if card, ok := c.StateCard(c.ThreadWithID(1)); !ok || card.Stale {
		t.Fatalf("the writer's own turn outdated its card: %+v %v", card, ok)
	}
	c.Update(1, func(th *model.Thread) {
		th.Busy = true
		th.Messages = append(th.Messages, model.Message{ID: 13, Role: "user", Content: "顺便把注册也看一下", SentAtMs: time.Now().UnixMilli() + 1000})
	})
	card, ok := storedStateCards(t, dir).Get(statecard.Key(agent, "session-a"))
	if !ok || !card.Stale || !strings.HasSuffix(statecard.Line(card), "Outdated") || !strings.Contains(statecard.Text(card), "Outdated: the conversation went on after it was written.") {
		t.Fatalf("card not outdated after the person wrote: %+v %v", card, ok)
	}
	// The next card replaces it whole.
	again := statusRequest(mcpproto.StatusArgs{Purpose: "修复登录过期并检查注册"})
	c.Update(1, func(th *model.Thread) {
		th.Messages = append(th.Messages, statusCall(agent, again, 14, "item-2"))
	})
	writeStateCard(t, c, again)
	card, ok = c.StateCard(c.ThreadWithID(1))
	if !ok || card.Stale || card.Purpose != "修复登录过期并检查注册" || len(card.Remaining) != 0 || card.Item != "item-2" {
		t.Fatalf("the new card did not replace the old one: %+v %v", card, ok)
	}
	if n := storedStateCards(t, dir).Len(); n != 1 {
		t.Fatalf("%d cards for one conversation", n)
	}
}

func TestStateCardIsOutdatedWhenCodexRunsAnotherTurn(t *testing.T) {
	agent := appmodel.AgentCodex
	c, dir := stateCardHost(t)
	r := statusRequest(mcpproto.StatusArgs{Purpose: "迁移配置"})
	c.SetThreads([]model.Thread{{ID: 1, Agent: agent, ServerID: "session-a", Busy: true, TurnID: "turn", Messages: []model.Message{statusCall(agent, r, 11, "item-1")}}})
	writeStateCard(t, c, r)
	c.Update(1, func(th *model.Thread) { th.Busy, th.TurnID = false, "" })
	if card, ok := c.StateCard(c.ThreadWithID(1)); !ok || card.Stale {
		t.Fatalf("the end of the writer's turn outdated its card: %+v %v", card, ok)
	}
	c.Update(1, func(th *model.Thread) { th.Busy, th.TurnID = true, "turn-2" })
	if card, ok := storedStateCards(t, dir).Get(statecard.Key(agent, "session-a")); !ok || !card.Stale {
		t.Fatalf("card not outdated by another turn: %+v %v", card, ok)
	}
}

func TestStateCardFollowsItsConversationToAnotherSession(t *testing.T) {
	agent := appmodel.AgentClaude
	c, dir := stateCardHost(t)
	r := statusRequest(mcpproto.StatusArgs{Purpose: "整理发布说明"})
	c.SetThreads([]model.Thread{{ID: 1, Agent: agent, ServerID: "session-a", Busy: true, Messages: []model.Message{statusCall(agent, r, 11, "item-1")}}})
	writeStateCard(t, c, r)
	c.Update(1, func(th *model.Thread) { th.ServerID = "session-b" })
	store := storedStateCards(t, dir)
	if _, ok := store.Get(statecard.Key(agent, "session-a")); ok {
		t.Fatal("the card stayed with the old session")
	}
	if card, ok := store.Get(statecard.Key(agent, "session-b")); !ok || card.Session != "session-b" || card.Purpose != "整理发布说明" || card.Stale {
		t.Fatalf("the card did not follow its conversation: %+v %v", card, ok)
	}
	if card, ok := c.StateCard(c.ThreadWithID(1)); !ok || card.Purpose != "整理发布说明" {
		t.Fatalf("the conversation lost its card: %+v %v", card, ok)
	}
}

func TestStateCardOfAnAutomationConversationIsRefused(t *testing.T) {
	agent := appmodel.AgentClaude
	c, dir := stateCardHost(t)
	r := statusRequest(mcpproto.StatusArgs{Purpose: "每日巡检", Done: []string{"检查了依赖更新"}})
	c.SetThreads([]model.Thread{{ID: 1, Agent: agent, ServerID: "session-a", Automation: "daily-check", Busy: true, Messages: []model.Message{statusCall(agent, r, 11, "item-1")}}})
	if _, err := c.MCP().StateCard(r); err == nil || !strings.Contains(err.Error(), "report_result") {
		t.Fatalf("an automation's conversation wrote a card: %v", err)
	}
	if n := storedStateCards(t, dir).Len(); n != 0 {
		t.Fatalf("%d cards written for an automation's conversation", n)
	}
}

// A card that does not fit is refused, not cut: its writer shortens it.
func TestStateCardThatDoesNotFitIsRefusedWhole(t *testing.T) {
	agent := appmodel.AgentClaude
	c, dir := stateCardHost(t)
	calledWith := func(r mcpproto.Request) {
		c.SetThreads([]model.Thread{{ID: 1, Agent: agent, ServerID: "session-a", Busy: true, Messages: []model.Message{statusCall(agent, r, 11, "item-1")}}})
	}
	many := make([]string, statecard.MaxItems+1)
	for i := range many {
		many[i] = fmt.Sprintf("第 %d 项", i+1)
	}
	unknown := mcpproto.Request{Tool: "update_status", Arguments: json.RawMessage(`{"purpose":"修复登录过期","mood":"fine"}`), Workspace: "/untrusted"}
	for _, tc := range []struct {
		name string
		r    mcpproto.Request
		want string
	}{
		{"no purpose", statusRequest(mcpproto.StatusArgs{Purpose: " \n ", Done: []string{"定位到时区换算"}}), "purpose is required"},
		{"long purpose", statusRequest(mcpproto.StatusArgs{Purpose: strings.Repeat("长", statecard.MaxText+1)}), "purpose must be at most 400 characters"},
		{"long question", statusRequest(mcpproto.StatusArgs{Purpose: "修复登录过期", WaitingFor: strings.Repeat("问", statecard.MaxText+1)}), "waiting_for must be at most 400 characters"},
		{"too many items", statusRequest(mcpproto.StatusArgs{Purpose: "修复登录过期", Remaining: many}), "remaining accepts at most 12 items"},
		{"long item", statusRequest(mcpproto.StatusArgs{Purpose: "修复登录过期", Checked: []string{strings.Repeat("x", statecard.MaxText+1)}}), "each item of checked must be at most"},
		{"unknown field", unknown, "unknown field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calledWith(tc.r)
			if _, err := c.MCP().StateCard(tc.r); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if n := storedStateCards(t, dir).Len(); n != 0 {
				t.Fatalf("%d cards written by a refused call", n)
			}
		})
	}
	// The limit counts characters, not bytes.
	fits := statusRequest(mcpproto.StatusArgs{Purpose: strings.Repeat("长", statecard.MaxText)})
	calledWith(fits)
	writeStateCard(t, c, fits)
	if n := storedStateCards(t, dir).Len(); n != 1 {
		t.Fatalf("%d cards after one that fits", n)
	}
}

// The agent's MCP process reaches the service over its socket; the service
// keeps the card, and drops it when a window deletes the conversation.
func TestStateCardThroughTheServiceIsDeletedWithItsConversation(t *testing.T) {
	f := newServiceFixture(t)
	id := collaborationCaller(t, f, "", "card-session")
	r := statusRequest(mcpproto.StatusArgs{Purpose: "核对发布清单", Remaining: []string{"写发布说明"}, WaitingFor: "是否今天发布"})
	f.serv.Do(func() {
		call := statusCall(appmodel.AgentCodex, r, f.C.NextID(), "item-1")
		f.C.Update(id, func(th *model.Thread) { th.Messages = append(th.Messages, call) })
	})
	client, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var saved string
	if err := client.Call("mcpStateCard", mcpproto.Request{Tool: r.Tool, Arguments: r.Arguments}, &saved); err != nil || !strings.Contains(saved, "State card saved") {
		t.Fatalf("card not saved through the service: %q %v", saved, err)
	}
	var card statecard.Card
	var ok bool
	f.serv.Do(func() { card, ok = f.C.StateCard(f.C.ThreadWithID(id)) })
	if !ok || card.Purpose != "核对发布清单" || !card.Waiting() || card.Session != "card-session" || card.Item != "item-1" {
		t.Fatalf("the service does not keep the card: %+v %v", card, ok)
	}
	if stored, ok := storedStateCards(t, statecard.Dir()).Get(card.Key()); !ok || stored.Purpose != card.Purpose {
		t.Fatalf("the card is not in the service's directory: %+v %v", stored, ok)
	}
	window := f.window()
	t.Cleanup(func() { window.Cancel(); window.Remote.Close() })
	f.win.Do(func() { window.DeleteThread(model.Thread{ID: id}) })
	for deadline := time.Now().Add(30 * time.Second); ok; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the card outlived its conversation")
		}
		f.serv.Do(func() { _, ok = f.C.StateCard(model.Thread{ServerID: "card-session"}) })
	}
	if _, ok := storedStateCards(t, statecard.Dir()).Get(card.Key()); ok {
		t.Fatal("the deleted conversation's card stayed in the directory")
	}
}
