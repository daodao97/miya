//go:build !mygo_ui

package integration

import (
	"encoding/json"
	codex "miya/internal/agent/codex/native"
	codexprojection "miya/internal/agent/codex/projection"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"strings"
	"testing"
)

func TestCountDiffLines(t *testing.T) {
	for _, tc := range []struct {
		name, diff string
		want       model.DiffLineStats
	}{
		{"empty", "", model.DiffLineStats{}},
		{"headerless", "+new\n-old\n context", model.DiffLineStats{Added: 1, Deleted: 1}},
		{"multiple files and signs in content", "--- a/a\n+++ b/a\n@@ -1,2 +1,3 @@\n--- removed\n+++ added\n+extra\n context\n--- a/b\n+++ /dev/null\n@@ -1 +0,0 @@\n-last\n\\ No newline at end of file\n", model.DiffLineStats{Added: 2, Deleted: 2}},
		{"metadata only", "diff --git a/a b/a\nold mode 100644\nnew mode 100755\nBinary files a/a and b/a differ\n", model.DiffLineStats{}},
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
	c.Handle(event("item/completed", `{"threadId":"t1","turnId":"turn1","item":{"id":"edit","type":"fileChange","status":"completed","changes":[{"path":"a.go","diff":"+new\n-old"}]}}`))
	for _, diff := range []string{"+new\n-old", strings.Repeat("+line\n", 2000) + "-old\n"} {
		raw, _ := json.Marshal(map[string]string{"threadId": "t1", "turnId": "turn1", "diff": diff})
		c.Handle(event("turn/diff/updated", string(raw)))
	}
	messages := c.Threads()[0].Messages
	if len(messages) != 2 || !strings.Contains(messages[1].Content, "[earlier output omitted]") {
		t.Fatalf("diff was not updated/bounded: %+v", messages)
	}
	if got := model.TurnDiffStats(messages); got != (model.DiffLineStats{Added: 2000, Deleted: 1}) {
		t.Fatalf("full diff count = %+v", got)
	}
	c.Handle(event("turn/diff/updated", `{"threadId":"t1","turnId":"turn1","diff":""}`))
	if got := model.TurnDiffStats(c.Threads()[0].Messages); got != (model.DiffLineStats{}) {
		t.Fatalf("reverted diff retained old edits: %+v", got)
	}
}

func TestTurnDiffStatsHistoryAndTurnBoundaries(t *testing.T) {
	var item codex.Item
	if err := json.Unmarshal([]byte(`{"type":"fileChange","status":"completed","changes":[{"path":"a","diff":"+new\n-old"},{"path":"b","diff":"+second"}]}`), &item); err != nil {
		t.Fatal(err)
	}
	m, _ := codexprojection.ItemMessage(item)
	m.TurnID = "history"
	failed := m
	failed.Status = "failed"
	running := m
	running.Status = "inProgress"
	messages := []model.Message{m, failed, running, {Kind: "diff", TurnID: "another", Content: "+next"}}
	if got := model.TurnDiffStats(messages); got != (model.DiffLineStats{Added: 3, Deleted: 1}) {
		t.Fatalf("history stats = %+v", got)
	}
	groups := msgfmt.GroupMessages(append([]model.Message{{Role: "user"}}, messages...))
	if len(groups) != 2 || groups[1].DiffStats != (model.DiffLineStats{Added: 3, Deleted: 1}) {
		t.Fatalf("group stats = %+v", groups)
	}
}
