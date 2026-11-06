//go:build !windows

package main

import (
	context "context"
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	controller "miya/internal/controller"
	remote "miya/internal/remote"
	os "os"
	filepath "path/filepath"
	reflect "reflect"
	strings "strings"
	testing "testing"
)

func TestCodexCacheDoesNotRestoreProcessState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cache := map[string][]appmodel.Thread{"/project": {
		{ID: 1, ServerID: "codex", Resumed: true},
		{ID: 2, ServerID: "claude", Agent: appmodel.AgentClaude, Resumed: true},
	}}
	// Read the format written by earlier releases, including its stale flag.
	data, err := json.Marshal(appmodel.ThreadListCache{Projects: cache})
	if err != nil {
		t.Fatal(err)
	}
	path := appmodel.ThreadListCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got := appmodel.LoadThreadListCache()["/project"]; got[0].Resumed || !got[1].Resumed {
		t.Fatalf("restored session state: %+v", got)
	}
	appmodel.WriteThreadListCache(cache)
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved appmodel.ThreadListCache
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if got := saved.Projects["/project"]; got[0].Resumed || !got[1].Resumed {
		t.Fatalf("persisted session state: %+v", got)
	}
}

func codexResumeFixture(t *testing.T) (*controller.Controller, *testDispatch, string) {
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
	c.Ctx, c.Cancel = context.WithCancel(context.Background())
	d := &testDispatch{}
	c.Run = d.push
	c.Serial = 1
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "resume-thread", Resumed: true}})
	c.ProjectCache[c.Cwd] = append([]appmodel.Thread(nil), c.Threads()...)
	t.Cleanup(func() {
		c.Closed = true
		c.Cancel()
		if c.Client != nil {
			c.Client.Close()
		}
	})
	c.Connect()
	waitCodexResume(t, c, d, func() bool { return c.Ready() && c.HistoryStatus() == "" && len(c.Models()) > 0 })
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
	c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "error"})
	waitCodexResume(t, c, d, func() bool {
		th := c.ThreadWithID(1)
		return !th.Busy && len(th.Messages) > 0 && th.Messages[len(th.Messages)-1].Role == "error"
	})
	before := c.ThreadWithID(1)
	var failedTurn string
	for _, m := range before.Messages {
		if m.Role == "user" {
			failedTurn = m.TurnID
		}
	}
	req := appmodel.RetryRequest{ThreadID: 1, MessageID: before.Messages[len(before.Messages)-1].ID}
	if err := c.RetryResponse(req); err != nil {
		t.Fatal(err)
	}
	if err := c.RetryResponse(req); err == nil {
		t.Fatal("duplicate retry was accepted")
	}
	waitCodexResume(t, c, d, func() bool {
		th := c.ThreadWithID(1)
		return !th.Busy && th.Messages[len(th.Messages)-1].Role == "error" && th.Messages[len(th.Messages)-1].ID != req.MessageID
	})
	after := c.ThreadWithID(1)
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
	c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "error"})
	waitCodexResume(t, c, d, func() bool {
		th := c.ThreadWithID(1)
		return !th.Busy && len(th.Messages) > 0 && th.Messages[len(th.Messages)-1].Role == "error"
	})
	before := c.ThreadWithID(1)
	if err := c.RetryResponse(appmodel.RetryRequest{ThreadID: 1, MessageID: before.Messages[len(before.Messages)-1].ID}); err != nil {
		t.Fatal(err)
	}
	waitCodexResume(t, c, d, func() bool {
		th := c.ThreadWithID(1)
		return !th.Busy && strings.Contains(th.Status, "cannot revert")
	})
	if after := c.ThreadWithID(1); !reflect.DeepEqual(after.Messages, before.Messages) || after.ServerID != before.ServerID {
		t.Fatal("a failed rewind discarded the original messages")
	}
	if methods := strings.Join(codexTurnRequests(t, logPath), ","); strings.Count(methods, "turn/start") != 1 {
		t.Fatal("retry sent a new message after the rewind failed")
	}
}

func sendCodexPhoneMessage(t *testing.T, c *controller.Controller, d *testDispatch, text string) {
	t.Helper()
	result := c.PhoneSend(remote.Request{Thread: 1, Text: text}, nil)
	if result.Thread != 1 || result.Err != "" {
		t.Fatalf("phone send: %+v", result)
	}
	waitCodexResume(t, c, d, func() bool {
		th := c.ThreadWithID(1)
		return !th.Busy && !th.Loading && len(th.Messages) > 0 && th.Messages[len(th.Messages)-1].Role == "assistant"
	})
	th := c.ThreadWithID(1)
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
	if c.ThreadWithID(1).Resumed || c.ProjectCache[c.Cwd][0].Resumed {
		t.Fatal("new process kept the old process's session")
	}
	sendCodexPhoneMessage(t, c, d, "after restart")
	if got := userTexts(c.ThreadWithID(1)); !reflect.DeepEqual(got, []string{"Earlier prompt", "after restart"}) {
		t.Fatalf("history was lost: %q", got)
	}
	// Losing the Codex connection must also leave other agents running.
	c.SetThreads(append(c.Threads(), appmodel.Thread{ID: 2, Agent: appmodel.AgentClaude, Busy: true, Resumed: true}))
	c.Client.Close()
	waitCodexResume(t, c, d, func() bool { return !c.Ready() })
	if c.ThreadWithID(1).Resumed || !c.ThreadWithID(2).Busy || !c.ThreadWithID(2).Resumed {
		t.Fatalf("disconnect affected the wrong sessions: %+v", c.Threads())
	}
	c.Connect()
	waitCodexResume(t, c, d, func() bool { return c.Ready() && c.HistoryStatus() == "" })
	sendCodexPhoneMessage(t, c, d, "after reconnect")
	want := []string{"thread/resume", "turn/start", "thread/resume", "turn/start"}
	if got := codexTurnRequests(t, logPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("request order %q, want %q", got, want)
	}
}

func TestCodexPhoneSendResumesAfterWorkspaceSwitch(t *testing.T) {
	c, d, logPath := codexResumeFixture(t)
	sendCodexPhoneMessage(t, c, d, "before switch")
	first, second := c.Cwd, t.TempDir()
	client := c.Client
	for _, path := range []string{second, first} {
		if err := c.ChangeWorkspace(path); err != nil {
			t.Fatal(err)
		}
		if c.Client != client || !c.ThreadWithID(1).Resumed {
			t.Fatal("switching project discarded the live Codex session")
		}
		waitCodexResume(t, c, d, func() bool { return c.Ready() && c.HistoryStatus() == "" })
	}
	sendCodexPhoneMessage(t, c, d, "after switch")
	want := []string{"thread/resume", "turn/start", "turn/start"}
	if got := codexTurnRequests(t, logPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("request order %q, want %q", got, want)
	}
}

func TestCodexWorkspaceSwitchKeepsTwoLiveTurns(t *testing.T) {
	c, d, _ := codexResumeFixture(t)
	first, second, client := c.Cwd, t.TempDir(), c.Client
	if c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "long"}) != 1 {
		t.Fatal("could not start first turn")
	}
	waitCodexResume(t, c, d, func() bool { return c.ThreadWithID(1).TurnID != "" })
	turn := c.ThreadWithID(1).TurnID
	if err := c.ChangeWorkspace(second); err != nil {
		t.Fatal(err)
	}
	if !(&controller.Service{C: c}).Busy() {
		t.Fatal("background turn must prevent service shutdown or upgrade")
	}
	id := c.Submit(appmodel.SendRequest{Text: "long", Agent: appmodel.AgentCodex})
	waitCodexResume(t, c, d, func() bool { return c.ThreadWithID(id).TurnID != "" && c.HistoryStatus() == "" })
	for _, path := range []string{first, second, first} {
		if err := c.ChangeWorkspace(path); err != nil {
			t.Fatal(err)
		}
		waitCodexResume(t, c, d, func() bool { return c.HistoryStatus() == "" })
		if c.Client != client || !c.ThreadWithID(1).Busy || c.ThreadWithID(1).TurnID != turn || !c.ThreadWithID(id).Busy {
			t.Fatalf("switch stopped a live turn: first %+v second %+v", c.ThreadWithID(1), c.ThreadWithID(id))
		}
	}
	// Stop only the background conversation through the real fixture RPC.
	c.StopThread(id)
	waitCodexResume(t, c, d, func() bool { return !c.ThreadWithID(id).Busy })
	if c.ThreadWithID(id).Status != "Stopped" || !c.ThreadWithID(1).Busy {
		t.Fatal("background stop affected the foreground turn")
	}
}

func TestCodexBackgroundApprovalDrainsQueueInOriginalProject(t *testing.T) {
	c, d, logPath := codexResumeFixture(t)
	first := c.Cwd
	c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "question"})
	waitCodexResume(t, c, d, func() bool { return len(c.Approvals()) == 1 })
	c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "queued follow-up"})
	if err := c.ChangeWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(c.Approvals()) != 1 {
		t.Fatal("switch discarded the pending background approval")
	}
	foreground := c.Submit(appmodel.SendRequest{Text: "long", Agent: appmodel.AgentCodex})
	waitCodexResume(t, c, d, func() bool { return c.ThreadWithID(foreground).TurnID != "" && c.HistoryStatus() == "" })
	c.Respond(c.Approvals()[0], map[string]any{"answers": map[string]any{}})
	waitCodexResume(t, c, d, func() bool {
		th := c.ThreadWithID(1)
		return !th.Busy && len(th.Queue) == 0 && len(userTexts(th)) == 3
	})
	if !c.ThreadWithID(foreground).Busy || len(c.Approvals()) != 0 {
		t.Fatal("background completion affected foreground or left a stale approval")
	}
	if c.ThreadWorkspace(1) != first {
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
			c.Update(1, func(th *appmodel.Thread) { th.Resumed = true })
			if tc.name == "recover" {
				sendCodexPhoneMessage(t, c, d, "recover this message")
			} else {
				const text = "keep my draft"
				paths := []string{"/tmp/notes.md"}
				var returned string
				c.OnDraftReturned = func(id int, text string, attachments []string) {
					if id != 1 || !reflect.DeepEqual(attachments, paths) {
						t.Errorf("draft returned to wrong conversation or lost attachments")
					}
					returned = text
				}
				if c.PhoneSend(remote.Request{Thread: 1, Text: text}, paths).Thread != 1 {
					t.Fatal("send was rejected")
				}
				waitCodexResume(t, c, d, func() bool { return returned != "" })
				th := c.ThreadWithID(1)
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
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "t1", Resumed: true, ReadOnly: true, HistoryLoaded: true,
		Messages: []appmodel.Message{{Role: "assistant", Content: "Saved reply"}}}})
	c.Handle(event("thread/closed", `{"threadId":"t1"}`))
	th := c.ThreadWithID(1)
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
		if appmodel.IsMissingCodexThread(err, "t1") {
			t.Fatalf("unsafe retry for error %v", err)
		}
	}
}

func TestCodexPhoneSendUsesStableIdentityAfterRenumbering(t *testing.T) {
	c, d, _ := codexResumeFixture(t)
	c.Serial = 77
	c.SetThreads([]appmodel.Thread{
		{ID: 1, ServerID: "different", Busy: true},
		{ID: 77, ServerID: "resume-thread"},
	})
	r := remote.Request{Op: "send", Thread: 1, ServerID: "resume-thread", Agent: appmodel.AgentCodex, Project: c.Cwd, Text: "after renumbering"}
	if res := c.PhoneSend(r, nil); res.Thread != 77 || res.Err != "" {
		t.Fatalf("stale number prevented sending to the original conversation: %+v", res)
	}
	waitCodexResume(t, c, d, func() bool {
		th := c.ThreadWithID(77)
		return !th.Busy && !th.Loading && len(th.Messages) > 2
	})
	if th := c.ThreadWithID(77); !reflect.DeepEqual(userTexts(th), []string{"Earlier prompt", r.Text}) {
		t.Fatalf("message went to the wrong conversation: %+v", th)
	}
	if th := c.ThreadWithID(1); !th.Busy || len(th.Messages) != 0 {
		t.Fatal("sending touched the conversation that reused the old number")
	}
}

func TestCodexPhoneSendRechecksReleasedWriter(t *testing.T) {
	c, d, logPath := codexResumeFixture(t)
	c.Update(1, func(t *appmodel.Thread) { t.ReadOnly = true; t.Resumed = false; t.HistoryLoaded = true })
	p := &controller.PhoneSendReq{Req: remote.Request{Thread: 1, ServerID: "resume-thread", Agent: appmodel.AgentCodex, Text: "after writer released"}}
	if result := p.Send(c); result.Thread != 0 || result.Err != "" || !p.OwnershipChecked || !c.ThreadWithID(1).Loading {
		t.Fatalf("ownership was not rechecked: %+v", result)
	}
	waitCodexResume(t, c, d, func() bool { return !c.ThreadWithID(1).Loading })
	if result := p.Send(c); result.Thread != 1 || result.Err != "" {
		t.Fatalf("released session stayed read-only: %+v", result)
	}
	waitCodexResume(t, c, d, func() bool { return !c.ThreadWithID(1).Busy })
	if got := strings.Join(codexTurnRequests(t, logPath), ","); got != "thread/resume,turn/start" {
		t.Fatalf("unexpected ownership/send requests: %s", got)
	}
}

func TestCodexPhoneSendKeepsActiveExternalWriterReadOnly(t *testing.T) {
	t.Setenv("QUICKGUI_FIXTURE_RESUME_ERROR", `{"code":-32600,"message":"thread resume-thread already has an active writer"}`)
	c, d, logPath := codexResumeFixture(t)
	c.Update(1, func(t *appmodel.Thread) { t.ReadOnly = true; t.Resumed = false; t.HistoryLoaded = true })
	p := &controller.PhoneSendReq{Req: remote.Request{Thread: 1, ServerID: "resume-thread", Text: "do not duplicate"}}
	p.Send(c)
	waitCodexResume(t, c, d, func() bool { return !c.ThreadWithID(1).Loading })
	for i := 0; i < 2; i++ {
		if result := p.Send(c); result.Thread != 0 || !strings.Contains(result.Err, "Read-only") {
			t.Fatalf("active writer protection bypassed: %+v", result)
		}
	}
	if got := strings.Join(codexTurnRequests(t, logPath), ","); got != "thread/resume" {
		t.Fatalf("retried ownership or sent despite conflict: %s", got)
	}
}
