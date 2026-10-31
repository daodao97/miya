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
	"time"

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

func waitCodexResume(t *testing.T, c *chatController, d *testDispatch, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		d.drain()
		if done() {
			return
		}
	}
	t.Fatalf("Codex fixture timed out: status %q, threads %+v", c.status(), c.threads())
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
	if c.submit(sendRequest{ThreadID: 1, Text: "long"}) != 1 {
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
	id := c.submit(sendRequest{Text: "long", Agent: agentCodex})
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
	c.submit(sendRequest{ThreadID: 1, Text: "question"})
	waitCodexResume(t, c, d, func() bool { return len(c.approvals()) == 1 })
	c.submit(sendRequest{ThreadID: 1, Text: "queued follow-up"})
	if err := c.changeWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(c.approvals()) != 1 {
		t.Fatal("switch discarded the pending background approval")
	}
	foreground := c.submit(sendRequest{Text: "long", Agent: agentCodex})
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
