package main

import (
	"encoding/json"
	"miya/internal/model"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testReferenceSource = "0123456789abcdef0123456789abcdef"

func testRef(backend, sid string) ThreadRef {
	return ThreadRef{Source: testReferenceSource, Backend: backend, SessionID: sid}
}
func testReference(t Thread) ThreadRef { return testRef(model.ThreadAgent(t), t.ServerID) }

func TestThreadReferenceURLIsExactAndLossless(t *testing.T) {
	for _, sid := range []string{"019a-uuid", "CaseSensitive", "中文 空格", "a/b?#%()"} {
		ref := testRef(agentCodex, sid)
		got, ok := parseThreadURL(ref.URL())
		if !ok || got != ref {
			t.Fatalf("URL round trip: %q %+v", ref.URL(), got)
		}
	}
	ref := testRef(agentCodex, "id")
	for _, raw := range []string{ref.URL() + "?secret=x", ref.URL() + "#fragment", ref.URL() + "/more", "miya://user@" + testReferenceSource + "/thread/codex/id", "miya://" + testReferenceSource + "/thread/unknown/id", "miya://desktop/thread/codex/id", "miya://" + testReferenceSource + "/thread/codex/"} {
		if _, ok := parseThreadURL(raw); ok {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestThreadReferencesRequireExplicitObjectsOutsideCode(t *testing.T) {
	ref := testRef(agentCodex, "id")
	mention := referenceText(ref, `同名 [标题] \ " 👋`)
	for _, raw := range []string{"@#2", ref.URL(), "`" + mention + "`", "``" + mention + "``", "\\" + mention, "```go\n" + mention + "\n```", "~~~\n" + mention + "\n~~~", "    " + mention, "\t" + mention, "> ~~~\n> " + mention + "\n> ~~~", "- ~~~\n  " + mention + "\n  ~~~"} {
		if got := threadRefs(raw); len(got) != 0 {
			t.Errorf("literal became reference: %q %v", raw, got)
		}
	}
	withBacktick := referenceText(ref, "title with `backtick") + referenceText(testRef(agentCodex, "next"), "next")
	if got := threadRefs(withBacktick); len(got) != 2 {
		t.Fatalf("title changed Markdown state: %v", got)
	}
	raw := "中文" + mention + mention + "。"
	if got := threadRefs(raw); len(got) != 1 || got[0] != ref {
		t.Fatal(got)
	}
	parts := messageParts(raw)
	if got, err := partsText(parts); err != nil || got != raw {
		t.Fatalf("parts round trip: %q %v", got, err)
	}
	data, _ := json.Marshal(parts)
	var restored []MessagePart
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if got, _ := partsText(restored); got != raw {
		t.Fatal(got)
	}
}

func TestReferenceSourceConcurrentInitialization(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			id, err := loadReferenceSource(dir)
			if err != nil {
				t.Error(err)
			}
			ids <- id
		})
	}
	wg.Wait()
	close(ids)
	want := ""
	for id := range ids {
		if want == "" {
			want = id
		}
		if id != want {
			t.Fatalf("different identities published: %s %s", want, id)
		}
	}
}

func TestReferenceSourcePersistsIndependentlyOfPairing(t *testing.T) {
	dir := t.TempDir()
	id, err := loadReferenceSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := loadReferenceSource(dir)
	if err != nil || id != again || !SourceIDPattern.MatchString(id) {
		t.Fatalf("source changed: %s %s %v", id, again, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reference-source"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReferenceSource(dir); err == nil {
		t.Fatal("silently replaced an invalid source identity")
	}
}

func TestReferenceIdentitySurvivesRenumberingAndRejectsOtherSources(t *testing.T) {
	c := testController()
	src := handoffSource()
	ref := testReference(src)
	src.ID = 999
	src.Title = "已改名"
	c.setThreads([]Thread{{ID: 2, ServerID: "wrong-session", HistoryLoaded: true}, src})
	if got, _, err := c.resolveReference(ref); err != nil || got.ServerID != src.ServerID {
		t.Fatalf("identity resolved by row number: %+v %v", got, err)
	}
	other := ref
	other.Source = "ffffffffffffffffffffffffffffffff"
	if _, _, err := c.resolveReference(other); err == nil {
		t.Fatal("cross-source reference resolved locally")
	}
	other = ref
	other.Backend = agentCodex
	if _, _, err := c.resolveReference(other); err == nil {
		t.Fatal("cross-backend reference resolved")
	}
}

func TestSameTitleReferencesKeepTheirIdentitiesWhenEdited(t *testing.T) {
	app, _ := testApp(t)
	a := handoffSource()
	a.Title = "同名"
	b := a
	b.ID = 3
	b.ServerID = "another-session"
	app.c.setThreads([]Thread{a, b})
	raw := referenceText(testReference(a), a.Title) + " 对比 " + referenceText(testReference(b), b.Title)
	out, _, err := expandThreadRefs(raw, ThreadRef{}, app.c.resolveReference)
	if err != nil {
		t.Fatal(err)
	}
	body, tags := app.threadReferenceContent(out)
	if body != raw || len(tags) != 2 || tags[0].Ref == tags[1].Ref {
		t.Fatalf("title merged identities: %s %+v", body, tags)
	}
	app.startEdit(Message{ID: 42, Content: out})
	if app.editText != raw {
		t.Fatal(app.editText)
	}
}

func TestForkKeepsOriginalReferenceAndMetadataAcrossRestart(t *testing.T) {
	c := testController()
	c.referenceDir = t.TempDir()
	src := handoffSource()
	ref := testReference(src)
	c.setThreads([]Thread{src})
	c.update(src.ID, func(t *Thread) {
		t.ServerID = "forked-session"
		t.Messages = []Message{{Role: "user", Content: "new branch"}}
	})
	got, messages, err := c.resolveReference(ref)
	if err != nil || got.ServerID != ref.SessionID || len(messages) != len(src.Messages) {
		t.Fatalf("reference followed fork: %+v %v", got, err)
	}
	read, err := c.mcpHandle("mcpRead", mcpRequest{Reference: ref.URL()})
	if err != nil || !strings.Contains(read.(mcpReadResult).Packet, "修复登录过期的问题") {
		t.Fatalf("original session link unreadable after fork: %v %v", read, err)
	}
	restarted := testController()
	restarted.referenceDir = c.referenceDir
	restarted.loadForkedReferences()
	if saved, ok := restarted.forkedReferences[ref]; !ok || saved.Ref.SessionID != src.ServerID {
		t.Fatalf("original metadata lost: %+v", restarted.forkedReferences)
	}
}

func TestInvalidReferencePreventsQueueAndPreservesDraft(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, ServerID: "target", Busy: true, HistoryLoaded: true}})
	c.setActive(1)
	raw := referenceText(testRef(agentCodex, "missing"), "不存在")
	c.rememberDraft(raw)
	c.send()
	if len(c.current().Queue) != 0 || c.draft() != raw || !strings.Contains(c.lastSubmitError, "不存在") {
		t.Fatalf("invalid reference sent: %+v %q %s", c.current().Queue, c.draft(), c.lastSubmitError)
	}
}

func TestReferenceSnapshotDoesNotCutLiteralContextTags(t *testing.T) {
	raw := "解释 <miya-thread-context 的含义，继续正文"
	if own, snapshots := splitReferenceContext(raw); own != raw || len(snapshots) != 0 {
		t.Fatal(own)
	}
	src := handoffSource()
	ref := testReference(src)
	raw += " " + referenceText(ref, src.Title)
	out, _, err := expandThreadRefs(raw, ThreadRef{}, func(ThreadRef) (Thread, []Message, error) { return src, src.Messages, nil })
	if err != nil {
		t.Fatal(err)
	}
	if own, snapshots := splitReferenceContext(out); own != raw || len(snapshots) != 1 {
		t.Fatal(own)
	}
	withAttachment, _ := attachmentDisplay(out, []string{"notes.txt"})
	if own, snapshots := splitReferenceContext(withAttachment); own != raw+"\n📎 notes.txt" || len(snapshots) != 1 {
		t.Fatalf("attachment exposed or truncated context: %q", own)
	}
	quoted := "```\n" + out + "\n```"
	if own, snapshots := splitReferenceContext(quoted); own != quoted || len(snapshots) != 0 {
		t.Fatal("code sample was hidden as a snapshot")
	}
	joined := joinReferenceMessages([]string{out, "接着做"})
	if own, snapshots := splitReferenceContext(joined); own != raw+"\n\n接着做" || len(snapshots) != 1 {
		t.Fatal(own)
	}
}
