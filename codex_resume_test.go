//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"miya/internal/codex"
	"miya/internal/remote"
)

func TestCodexCacheDoesNotRestoreProcessState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cache := map[string][]Thread{"/project": {
		{ID: 1, ServerID: "codex", Resumed: true},
		{ID: 2, ServerID: "claude", Agent: agentClaude, Resumed: true},
	}}
	// Read the format written by earlier releases, including its stale flag.
	data, err := json.Marshal(threadListCache{Projects: cache})
	if err != nil {
		t.Fatal(err)
	}
	path := threadListCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadThreadListCache()["/project"]; got[0].Resumed || !got[1].Resumed {
		t.Fatalf("restored session state: %+v", got)
	}
	writeThreadListCache(cache)
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved threadListCache
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if got := saved.Projects["/project"]; got[0].Resumed || !got[1].Resumed {
		t.Fatalf("persisted session state: %+v", got)
	}
}

func codexResumeFixture(t *testing.T) (*chatController, *testDispatch, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	bin, err := filepath.Abs("testdata/codex-server.py")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")
	t.Setenv("CODEX_BINARY", bin)
	t.Setenv("QUICKGUI_FIXTURE_RESUME", "1")
	t.Setenv("QUICKGUI_FIXTURE_LOG", logPath)
	c := serverController(t.TempDir(), "")
	c.ctx, c.cancel = context.WithCancel(context.Background())
	d := &testDispatch{}
	c.run = d.push
	c.serial = 1
	c.setThreads([]Thread{{ID: 1, ServerID: "resume-thread", Resumed: true}})
	c.projectCache[c.cwd] = append([]Thread(nil), c.threads()...)
	t.Cleanup(func() {
		c.closed = true
		c.cancel()
		if c.client != nil {
			c.client.Close()
		}
	})
	c.connect()
	waitCodexResume(t, c, d, func() bool { return c.ready() && c.historyStatus() == "" && len(c.models()) > 0 })
	return c, d, logPath
}

func codexTurnRequests(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var req struct{ Method string }
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatal(err)
		}
		switch req.Method {
		case "thread/start", "thread/resume", "turn/start":
			methods = append(methods, req.Method)
		}
	}
	return methods
}

func TestCodexRetriesFailedTurnFromOriginalMessage(t *testing.T) {
	c, d, logPath := codexResumeFixture(t)
	c.submit(SendRequest{ThreadID: 1, Text: "error"})
	waitCodexResume(t, c, d, func() bool {
		th := c.threadWithID(1)
		return !th.Busy && len(th.Messages) > 0 && th.Messages[len(th.Messages)-1].Role == "error"
	})
	before := c.threadWithID(1)
	var failedTurn string
	for _, m := range before.Messages {
		if m.Role == "user" {
			failedTurn = m.TurnID
		}
	}
	req := retryRequest{ThreadID: 1, MessageID: before.Messages[len(before.Messages)-1].ID}
	if err := c.retryResponse(req); err != nil {
		t.Fatal(err)
	}
	if err := c.retryResponse(req); err == nil {
		t.Fatal("duplicate retry was accepted")
	}
	waitCodexResume(t, c, d, func() bool {
		th := c.threadWithID(1)
		return !th.Busy && th.Messages[len(th.Messages)-1].Role == "error" && th.Messages[len(th.Messages)-1].ID != req.MessageID
	})
	after := c.threadWithID(1)
	if after.ServerID != before.ServerID || after.Messages[0].Content != before.Messages[0].Content {
		t.Fatal("retry replaced the original session or history")
	}
	if len(after.Messages) != len(before.Messages) || strings.Join(userTexts(after), ",") != "Earlier prompt,error" {
		t.Fatalf("retry appended a continuation or kept the failed turn: %+v", after.Messages)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var replay []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r struct {
			Method string
			Params struct {
				BeforeTurnID string                  `json:"beforeTurnId"`
				Input        []struct{ Text string } `json:"input"`
			}
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		switch r.Method {
		case "thread/revert":
			if r.Params.BeforeTurnID != failedTurn {
				t.Fatal("retry reverted the wrong turn")
			}
			replay = append(replay, "revert")
		case "turn/start":
			replay = append(replay, r.Params.Input[0].Text)
		}
	}
	if strings.Join(replay, ",") != "error,revert,error" {
		t.Fatalf("retry did not revert before replaying the original request: %v", replay)
	}
	methods := strings.Join(codexTurnRequests(t, logPath), ",")
	if strings.Count(methods, "turn/start") != 2 || strings.Contains(methods, "thread/start") {
		t.Fatalf("retry sent unexpected requests: %s", methods)
	}
}

func TestCodexRetryKeepsHistoryWhenRevertFails(t *testing.T) {
	t.Setenv("QUICKGUI_FIXTURE_REVERT_ERROR", `{"code":-32600,"message":"cannot revert"}`)
	c, d, logPath := codexResumeFixture(t)
	c.submit(SendRequest{ThreadID: 1, Text: "error"})
	waitCodexResume(t, c, d, func() bool {
		th := c.threadWithID(1)
		return !th.Busy && len(th.Messages) > 0 && th.Messages[len(th.Messages)-1].Role == "error"
	})
	before := c.threadWithID(1)
	if err := c.retryResponse(retryRequest{ThreadID: 1, MessageID: before.Messages[len(before.Messages)-1].ID}); err != nil {
		t.Fatal(err)
	}
	waitCodexResume(t, c, d, func() bool {
		th := c.threadWithID(1)
		return !th.Busy && strings.Contains(th.Status, "cannot revert")
	})
	if after := c.threadWithID(1); !reflect.DeepEqual(after.Messages, before.Messages) || after.ServerID != before.ServerID {
		t.Fatal("a failed rewind discarded the original messages")
	}
	if methods := strings.Join(codexTurnRequests(t, logPath), ","); strings.Count(methods, "turn/start") != 1 {
		t.Fatal("retry sent a new message after the rewind failed")
	}
}

func sendCodexPhoneMessage(t *testing.T, c *chatController, d *testDispatch, text string) {
	t.Helper()
	result := c.phoneSend(remote.Request{Thread: 1, Text: text}, nil)
	if result.thread != 1 || result.err != "" {
		t.Fatalf("phone send: %+v", result)
	}
	waitCodexResume(t, c, d, func() bool {
		th := c.threadWithID(1)
		return !th.Busy && !th.Loading && len(th.Messages) > 0 && th.Messages[len(th.Messages)-1].Role == "assistant"
	})
	th := c.threadWithID(1)
	if th.ServerID != "resume-thread" || !th.Resumed || th.Status != "" {
		t.Fatalf("wrong conversation after sending: %+v", th)
	}
	count := 0
	for _, m := range th.Messages {
		if m.Role == "error" {
			t.Fatalf("send error: %s", m.Content)
		}
		if m.Role == "user" && m.Content == text {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("phone message appeared %d times", count)
	}
}

func TestCodexPhoneSendResumesAfterRestartAndReconnect(t *testing.T) {
	c, d, logPath := codexResumeFixture(t)
	if c.threadWithID(1).Resumed || c.projectCache[c.cwd][0].Resumed {
		t.Fatal("new process kept the old process's session")
	}
	sendCodexPhoneMessage(t, c, d, "after restart")
	if got := userTexts(c.threadWithID(1)); !reflect.DeepEqual(got, []string{"Earlier prompt", "after restart"}) {
		t.Fatalf("history was lost: %q", got)
	}
	// Losing the Codex connection must also leave other agents running.
	c.setThreads(append(c.threads(), Thread{ID: 2, Agent: agentClaude, Busy: true, Resumed: true}))
	c.client.Close()
	waitCodexResume(t, c, d, func() bool { return !c.ready() })
	if c.threadWithID(1).Resumed || !c.threadWithID(2).Busy || !c.threadWithID(2).Resumed {
		t.Fatalf("disconnect affected the wrong sessions: %+v", c.threads())
	}
	c.connect()
	waitCodexResume(t, c, d, func() bool { return c.ready() && c.historyStatus() == "" })
	sendCodexPhoneMessage(t, c, d, "after reconnect")
	want := []string{"thread/resume", "turn/start", "thread/resume", "turn/start"}
	if got := codexTurnRequests(t, logPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("request order %q, want %q", got, want)
	}
}

func TestCodexPhoneSendResumesAfterWorkspaceSwitch(t *testing.T) {
	c, d, logPath := codexResumeFixture(t)
	sendCodexPhoneMessage(t, c, d, "before switch")
	first, second := c.cwd, t.TempDir()
	client := c.client
	for _, path := range []string{second, first} {
		if err := c.changeWorkspace(path); err != nil {
			t.Fatal(err)
		}
		if c.client != client || !c.threadWithID(1).Resumed {
			t.Fatal("switching project discarded the live Codex session")
		}
		waitCodexResume(t, c, d, func() bool { return c.ready() && c.historyStatus() == "" })
	}
	sendCodexPhoneMessage(t, c, d, "after switch")
	want := []string{"thread/resume", "turn/start", "turn/start"}
	if got := codexTurnRequests(t, logPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("request order %q, want %q", got, want)
	}
}

func TestCodexWorkspaceSwitchKeepsTwoLiveTurns(t *testing.T) {
	c, d, _ := codexResumeFixture(t)
	first, second, client := c.cwd, t.TempDir(), c.client
	if c.submit(SendRequest{ThreadID: 1, Text: "long"}) != 1 {
		t.Fatal("could not start first turn")
	}
	waitCodexResume(t, c, d, func() bool { return c.threadWithID(1).TurnID != "" })
	turn := c.threadWithID(1).TurnID
	if err := c.changeWorkspace(second); err != nil {
		t.Fatal(err)
	}
	if !(&service{c: c}).busy() {
		t.Fatal("background turn must prevent service shutdown or upgrade")
	}
	id := c.submit(SendRequest{Text: "long", Agent: agentCodex})
	waitCodexResume(t, c, d, func() bool { return c.threadWithID(id).TurnID != "" && c.historyStatus() == "" })
	for _, path := range []string{first, second, first} {
		if err := c.changeWorkspace(path); err != nil {
			t.Fatal(err)
		}
		waitCodexResume(t, c, d, func() bool { return c.historyStatus() == "" })
		if c.client != client || !c.threadWithID(1).Busy || c.threadWithID(1).TurnID != turn || !c.threadWithID(id).Busy {
			t.Fatalf("switch stopped a live turn: first %+v second %+v", c.threadWithID(1), c.threadWithID(id))
		}
	}
	// Stop only the background conversation through the real fixture RPC.
	c.stopThread(id)
	waitCodexResume(t, c, d, func() bool { return !c.threadWithID(id).Busy })
	if c.threadWithID(id).Status != "Stopped" || !c.threadWithID(1).Busy {
		t.Fatal("background stop affected the foreground turn")
	}
}

func TestCodexBackgroundApprovalDrainsQueueInOriginalProject(t *testing.T) {
	c, d, logPath := codexResumeFixture(t)
	first := c.cwd
	c.submit(SendRequest{ThreadID: 1, Text: "question"})
	waitCodexResume(t, c, d, func() bool { return len(c.approvals()) == 1 })
	c.submit(SendRequest{ThreadID: 1, Text: "queued follow-up"})
	if err := c.changeWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(c.approvals()) != 1 {
		t.Fatal("switch discarded the pending background approval")
	}
	foreground := c.submit(SendRequest{Text: "long", Agent: agentCodex})
	waitCodexResume(t, c, d, func() bool { return c.threadWithID(foreground).TurnID != "" && c.historyStatus() == "" })
	c.respond(c.approvals()[0], map[string]any{"answers": map[string]any{}})
	waitCodexResume(t, c, d, func() bool {
		th := c.threadWithID(1)
		return !th.Busy && len(th.Queue) == 0 && len(userTexts(th)) == 3
	})
	if !c.threadWithID(foreground).Busy || len(c.approvals()) != 0 {
		t.Fatal("background completion affected foreground or left a stale approval")
	}
	if c.threadWorkspace(1) != first {
		t.Fatal("queued message changed workspace")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r struct {
			Method string
			Params struct {
				ThreadID string `json:"threadId"`
				Cwd      string `json:"cwd"`
			}
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if r.Method == "turn/start" && r.Params.ThreadID == "resume-thread" && r.Params.Cwd != first {
			t.Fatalf("background turn used wrong cwd: %q", r.Params.Cwd)
		}
	}
}

func TestCodexSendRecoversMissingLiveThreadOnce(t *testing.T) {
	for _, tc := range []struct {
		name, resumeError, turnError string
		want                         []string
	}{
		{"recover", "", "", []string{"turn/start", "thread/resume", "turn/start"}},
		{"missing history", `{"code":-32600,"message":"no rollout found"}`, "", []string{"turn/start", "thread/resume"}},
		{"writer conflict", `{"code":-32600,"message":"thread resume-thread already has an active writer"}`, "", []string{"turn/start", "thread/resume"}},
		{"still missing", "", `{"code":-32600,"message":"thread not found: resume-thread"}`, []string{"turn/start", "thread/resume", "turn/start"}},
		{"other error", "", `{"code":-32000,"message":"rate limit exceeded"}`, []string{"turn/start"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("QUICKGUI_FIXTURE_RESUME_ERROR", tc.resumeError)
			t.Setenv("QUICKGUI_FIXTURE_TURN_ERROR", tc.turnError)
			c, d, logPath := codexResumeFixture(t)
			// Simulate a thread evicted from the running server without a
			// thread/closed notification reaching this client.
			c.update(1, func(th *Thread) { th.Resumed = true })
			if tc.name == "recover" {
				sendCodexPhoneMessage(t, c, d, "recover this message")
			} else {
				const text = "keep my draft"
				paths := []string{"/tmp/notes.md"}
				var returned string
				c.onDraftReturned = func(id int, text string, attachments []string) {
					if id != 1 || !reflect.DeepEqual(attachments, paths) {
						t.Errorf("draft returned to wrong conversation or lost attachments")
					}
					returned = text
				}
				if c.phoneSend(remote.Request{Thread: 1, Text: text}, paths).thread != 1 {
					t.Fatal("send was rejected")
				}
				waitCodexResume(t, c, d, func() bool { return returned != "" })
				th := c.threadWithID(1)
				if returned != text || th.Draft != text || th.Busy || th.ServerID != "resume-thread" {
					t.Fatalf("failed send lost the draft or conversation: %+v", th)
				}
				if tc.name != "other error" && th.Resumed {
					t.Fatal("failed recovery retained the stale session flag")
				}
			}
			if got := codexTurnRequests(t, logPath); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("request order %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCodexClosedThreadMustResumeBeforeSending(t *testing.T) {
	c := testController()
	c.setThreads([]Thread{{ID: 1, ServerID: "t1", Resumed: true, ReadOnly: true, HistoryLoaded: true,
		Messages: []Message{{Role: "assistant", Content: "Saved reply"}}}})
	c.handle(event("thread/closed", `{"threadId":"t1"}`))
	th := c.threadWithID(1)
	if th.Resumed || th.ReadOnly || !th.HistoryLoaded || len(th.Messages) != 1 {
		t.Fatalf("closed session lost history or kept ownership: %+v", th)
	}
}

func TestMissingCodexThreadErrorIsSpecific(t *testing.T) {
	for _, err := range []error{nil,
		&codex.RPCError{Code: -32000, Message: "thread not found: t1"},
		&codex.RPCError{Code: -32600, Message: "thread not found: t2"},
		&codex.RPCError{Code: -32600, Message: "no rollout found for thread id t1"},
	} {
		if isMissingCodexThread(err, "t1") {
			t.Fatalf("unsafe retry for error %v", err)
		}
	}
}

func TestCodexPhoneSendUsesStableIdentityAfterRenumbering(t *testing.T) {
	c, d, _ := codexResumeFixture(t)
	c.serial = 77
	c.setThreads([]Thread{
		{ID: 1, ServerID: "different", Busy: true},
		{ID: 77, ServerID: "resume-thread"},
	})
	r := remote.Request{Op: "send", Thread: 1, ServerID: "resume-thread", Agent: agentCodex, Project: c.cwd, Text: "after renumbering"}
	if res := c.phoneSend(r, nil); res.thread != 77 || res.err != "" {
		t.Fatalf("stale number prevented sending to the original conversation: %+v", res)
	}
	waitCodexResume(t, c, d, func() bool {
		th := c.threadWithID(77)
		return !th.Busy && !th.Loading && len(th.Messages) > 2
	})
	if th := c.threadWithID(77); !reflect.DeepEqual(userTexts(th), []string{"Earlier prompt", r.Text}) {
		t.Fatalf("message went to the wrong conversation: %+v", th)
	}
	if th := c.threadWithID(1); !th.Busy || len(th.Messages) != 0 {
		t.Fatal("sending touched the conversation that reused the old number")
	}
}

func TestCodexPhoneSendRechecksReleasedWriter(t *testing.T) {
	c, d, logPath := codexResumeFixture(t)
	c.update(1, func(t *Thread) { t.ReadOnly = true; t.Resumed = false; t.HistoryLoaded = true })
	p := &phoneSendReq{req: remote.Request{Thread: 1, ServerID: "resume-thread", Agent: agentCodex, Text: "after writer released"}}
	if result := p.send(c); result.thread != 0 || result.err != "" || !p.ownershipChecked || !c.threadWithID(1).Loading {
		t.Fatalf("ownership was not rechecked: %+v", result)
	}
	waitCodexResume(t, c, d, func() bool { return !c.threadWithID(1).Loading })
	if result := p.send(c); result.thread != 1 || result.err != "" {
		t.Fatalf("released session stayed read-only: %+v", result)
	}
	waitCodexResume(t, c, d, func() bool { return !c.threadWithID(1).Busy })
	if got := strings.Join(codexTurnRequests(t, logPath), ","); got != "thread/resume,turn/start" {
		t.Fatalf("unexpected ownership/send requests: %s", got)
	}
}

func TestCodexPhoneSendKeepsActiveExternalWriterReadOnly(t *testing.T) {
	t.Setenv("QUICKGUI_FIXTURE_RESUME_ERROR", `{"code":-32600,"message":"thread resume-thread already has an active writer"}`)
	c, d, logPath := codexResumeFixture(t)
	c.update(1, func(t *Thread) { t.ReadOnly = true; t.Resumed = false; t.HistoryLoaded = true })
	p := &phoneSendReq{req: remote.Request{Thread: 1, ServerID: "resume-thread", Text: "do not duplicate"}}
	p.send(c)
	waitCodexResume(t, c, d, func() bool { return !c.threadWithID(1).Loading })
	for i := 0; i < 2; i++ {
		if result := p.send(c); result.thread != 0 || !strings.Contains(result.err, "Read-only") {
			t.Fatalf("active writer protection bypassed: %+v", result)
		}
	}
	if got := strings.Join(codexTurnRequests(t, logPath), ","); got != "thread/resume" {
		t.Fatalf("retried ownership or sent despite conflict: %s", got)
	}
}
