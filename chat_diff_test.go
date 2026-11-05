package main

import (
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/codex"
	"miya/internal/model"

	"github.com/egoist/mygo/ui"
)

func TestCountDiffLines(t *testing.T) {
	for _, tc := range []struct {
		name, diff string
		want       DiffLineStats
	}{
		{"empty", "", DiffLineStats{}},
		{"headerless", "+new\n-old\n context", DiffLineStats{Added: 1, Deleted: 1}},
		{"multiple files and signs in content", "--- a/a\n+++ b/a\n@@ -1,2 +1,3 @@\n--- removed\n+++ added\n+extra\n context\n--- a/b\n+++ /dev/null\n@@ -1 +0,0 @@\n-last\n\\ No newline at end of file\n", DiffLineStats{Added: 2, Deleted: 2}},
		{"metadata only", "diff --git a/a b/a\nold mode 100644\nnew mode 100755\nBinary files a/a and b/a differ\n", DiffLineStats{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := model.CountDiffLines(tc.diff); got != tc.want {
				t.Fatalf("stats = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTurnDiffStatsPreferLatestFullDiff(t *testing.T) {
	c := testController()
	// Tool and turn diff report the same edit; neither repeated updates nor
	// the bounded activity preview should alter the count.
	c.handle(event("item/completed", `{"threadId":"t1","turnId":"turn1","item":{"id":"edit","type":"fileChange","status":"completed","changes":[{"path":"a.go","diff":"+new\n-old"}]}}`))
	for _, diff := range []string{"+new\n-old", strings.Repeat("+line\n", 2000) + "-old\n"} {
		raw, _ := json.Marshal(map[string]string{"threadId": "t1", "turnId": "turn1", "diff": diff})
		c.handle(event("turn/diff/updated", string(raw)))
	}
	messages := c.threads()[0].Messages
	if len(messages) != 2 || !strings.Contains(messages[1].Content, "[earlier output omitted]") {
		t.Fatalf("diff was not updated/bounded: %+v", messages)
	}
	if got := model.TurnDiffStats(messages); got != (DiffLineStats{Added: 2000, Deleted: 1}) {
		t.Fatalf("full diff count = %+v", got)
	}
	c.handle(event("turn/diff/updated", `{"threadId":"t1","turnId":"turn1","diff":""}`))
	if got := model.TurnDiffStats(c.threads()[0].Messages); got != (DiffLineStats{}) {
		t.Fatalf("reverted diff retained old edits: %+v", got)
	}
}

func TestTurnDiffStatsHistoryAndTurnBoundaries(t *testing.T) {
	var item codex.Item
	if err := json.Unmarshal([]byte(`{"type":"fileChange","status":"completed","changes":[{"path":"a","diff":"+new\n-old"},{"path":"b","diff":"+second"}]}`), &item); err != nil {
		t.Fatal(err)
	}
	m, _ := itemMessage(item)
	m.TurnID = "history"
	failed := m
	failed.Status = "failed"
	running := m
	running.Status = "inProgress"
	messages := []Message{m, failed, running, {Kind: "diff", TurnID: "another", Content: "+next"}}
	if got := model.TurnDiffStats(messages); got != (DiffLineStats{Added: 3, Deleted: 1}) {
		t.Fatalf("history stats = %+v", got)
	}
	groups := groupMessages(append([]Message{{Role: "user"}}, messages...))
	if len(groups) != 2 || groups[1].DiffStats != (DiffLineStats{Added: 3, Deleted: 1}) {
		t.Fatalf("group stats = %+v", groups)
	}
}

func TestCompletedResponseShowsDiffStats(t *testing.T) {
	app, tt := testApp(t)
	app.c.setThreads([]Thread{{ID: 1, Loaded: true, HistoryLoaded: true, Messages: []Message{
		{ID: 1, Role: "assistant", Content: "Done", StartedAtMs: 1000, CompletedAtMs: 12500},
		{ID: 2, Role: "tool", Kind: "diff", Content: "+one\n+two\n-old"},
	}}})
	app.c.setActive(1)
	tt.Frame()
	for _, want := range []string{"Worked for 11s", "+2", "−1"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	status, _ := tt.Find("Worked for 11s")
	added, _ := tt.Find("+2")
	deleted, _ := tt.Find("−1")
	copy, _ := tt.Find("Copy response")
	if added.X < status.X+status.W || deleted.X < added.X+added.W || copy.X < deleted.X+deleted.W {
		t.Fatalf("latest diff stats are not between elapsed time and actions: %v / %v / %v / %v", status, added, deleted, copy)
	}
	for _, stat := range []ui.Rect{added, deleted} {
		if middle := status.Y + status.H/2; middle < stat.Y || middle > stat.Y+stat.H {
			t.Fatalf("latest diff stats stayed below the reply: %v / %v", status, stat)
		}
	}
	// The phone shares the same response footer, including its action buttons.
	app.mobile = true
	phone := ui.NewTester(func(c *ui.Context) { app.groupView(c, groupMessages(app.c.current().Messages)[0]) }, 320, 200)
	phone.Frame()
	for _, label := range []string{"+2", "−1", "Copy response", "Hand off to a new conversation"} {
		r, ok := phone.Find(label)
		if !ok || r.X < 0 || r.X+r.W > 320 {
			t.Fatalf("phone footer overflow: %s %v", label, r)
		}
	}
	app.c.update(1, func(th *Thread) { th.Messages = th.Messages[:1] })
	phone.Frame()
	if !phone.HasText("Worked for 11s") {
		t.Fatal("elapsed time disappeared without a diff")
	}
	for _, label := range []string{"·", "+0", "−0", "+2", "−1"} {
		if phone.HasText(label) {
			t.Fatalf("diff footer visible without changes: %q", phone.Texts())
		}
	}
}
