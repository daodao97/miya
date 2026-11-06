//go:build !windows

package main

import (
	appmodel "miya/internal/appmodel"
	mobile "miya/internal/mobile"
	remote "miya/internal/remote"
	uiruntime "miya/internal/uiruntime"
	os "os"
	filepath "path/filepath"
	testing "testing"
)

func TestPhoneClaudeSendPreservesReadOnlyHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := t.TempDir()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, cwd, "phone-history",
		`{"type":"user","uuid":"u1","message":{"content":"Earlier question"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"content":[{"type":"text","text":"Earlier answer"}]}}`)
	c := serverController(cwd, bin)
	c.Serial = 100
	c.SetReady(true)
	c.SetThreads([]appmodel.Thread{{ID: 7, Agent: appmodel.AgentClaude, ServerID: "phone-history", Resumed: true}})
	conn, ex := serveTestPhone(t, c)
	m := mobile.NewMobileApp()
	r := remote.Request{Op: "subscribe", Thread: 7, Agent: appmodel.AgentClaude, ServerID: "phone-history", Project: cwd}
	conn.requests <- r
	if messages := phoneOf(t, m, conn.nextThread(t)); len(messages) != 2 {
		t.Fatalf("initial phone history: %+v", messages)
	}
	ex.Do(func() {
		if c.ThreadWithID(7).HistoryLoaded || len(c.ThreadWithID(7).Messages) != 0 {
			t.Error("phone viewing should reproduce a controller with metadata only")
		}
	})
	r.Op, r.Text = "send", "Follow-up"
	conn.requests <- r
	for {
		d := conn.nextThread(t)
		messages := phoneOf(t, m, d)
		if len(messages) < 2 || messages[0].Content != "Earlier question" || messages[1].Content != "Earlier answer" {
			t.Fatalf("sending erased the phone's history: %+v", messages)
		}
		if !d.Thread.Busy && len(messages) >= 4 {
			users := userTexts(m.Viewer.Current())
			if len(users) != 2 || users[1] != "Follow-up" {
				t.Fatalf("follow-up was lost or duplicated: %q", users)
			}
			break
		}
	}
}

func TestClaudeHistoryLoadDrainsQueueAfterProjectSwitch(t *testing.T) {
	c, d := queueController(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Serial = 100
	cwd := c.Cwd
	writeClaudeSession(t, cwd, "s1", `{"type":"user","uuid":"u1","message":{"content":"Earlier question"}}`)
	c.Update(1, func(th *appmodel.Thread) { th.Busy, th.HistoryLoaded = false, false })
	c.LoadClaudeHistory(c.ThreadWithID(1))
	if c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "Queued follow-up"}) != 1 {
		t.Fatal("follow-up was not accepted while history was loading")
	}
	c.ProjectCache = map[string][]appmodel.Thread{cwd: c.Threads()}
	c.Cwd = t.TempDir()
	c.SetThreads(nil)
	waitClaudeHistory(c)
	d.drain()
	th := c.ThreadWithID(1)
	users := userTexts(th)
	if th.Loading || !th.HistoryLoaded || len(th.Queue) != 0 || len(users) != 2 || users[0] != "Earlier question" || users[1] != "Queued follow-up" {
		t.Fatalf("background history and queued send: %+v", th)
	}
}
