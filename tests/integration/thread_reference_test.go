package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	mcpproto "miya/internal/mcp/protocol"
)

const testReferenceSource = "0123456789abcdef0123456789abcdef"

func testRef(backend, sid string) model.ThreadRef {
	return model.ThreadRef{Source: testReferenceSource, Backend: backend, SessionID: sid}
}

func testReference(t model.Thread) model.ThreadRef {
	return testRef(model.ThreadAgent(t), t.ServerID)
}

func TestThreadReferenceURLIsExactAndLossless(t *testing.T) {
	for _, sid := range []string{"019a-uuid", "CaseSensitive", "中文 空格", "a/b?#%()"} {
		ref := testRef(appmodel.AgentCodex, sid)
		got, ok := model.ParseThreadURL(ref.URL())
		if !ok || got != ref {
			t.Fatalf("URL round trip: %q %+v", ref.URL(), got)
		}
	}
	ref := testRef(appmodel.AgentCodex, "id")
	for _, raw := range []string{ref.URL() + "?secret=x", ref.URL() + "#fragment", ref.URL() + "/more", "miya://user@" + testReferenceSource + "/thread/codex/id", "miya://" + testReferenceSource + "/thread/unknown/id", "miya://desktop/thread/codex/id", "miya://" + testReferenceSource + "/thread/codex/"} {
		if _, ok := model.ParseThreadURL(raw); ok {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestThreadReferencesRequireExplicitObjectsOutsideCode(t *testing.T) {
	ref := testRef(appmodel.AgentCodex, "id")
	mention := model.ReferenceText(ref, `同名 [标题] \ " 👋`)
	for _, raw := range []string{"@#2", ref.URL(), "`" + mention + "`", "``" + mention + "``", "\\" + mention, "```go\n" + mention + "\n```", "~~~\n" + mention + "\n~~~", "    " + mention, "\t" + mention, "> ~~~\n> " + mention + "\n> ~~~", "- ~~~\n  " + mention + "\n  ~~~"} {
		if got := msgfmt.ThreadRefs(raw); len(got) != 0 {
			t.Errorf("literal became reference: %q %v", raw, got)
		}
	}
	withBacktick := model.ReferenceText(ref, "title with `backtick") + model.ReferenceText(testRef(appmodel.AgentCodex, "next"), "next")
	if got := msgfmt.ThreadRefs(withBacktick); len(got) != 2 {
		t.Fatalf("title changed Markdown state: %v", got)
	}
	raw := "中文" + mention + mention + "。"
	if got := msgfmt.ThreadRefs(raw); len(got) != 1 || got[0] != ref {
		t.Fatal(got)
	}
	parts := model.MessageParts(raw)
	if got, err := composer.PartsText(parts); err != nil || got != raw {
		t.Fatalf("parts round trip: %q %v", got, err)
	}
	data, _ := json.Marshal(parts)
	var restored []model.MessagePart
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if got, _ := composer.PartsText(restored); got != raw {
		t.Fatal(got)
	}
}

func TestReferenceSourceConcurrentInitialization(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			id, err := model.LoadReferenceSource(dir)
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
	id, err := model.LoadReferenceSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := model.LoadReferenceSource(dir)
	if err != nil || id != again || !model.SourceIDPattern.MatchString(id) {
		t.Fatalf("source changed: %s %s %v", id, again, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reference-source"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := model.LoadReferenceSource(dir); err == nil {
		t.Fatal("silently replaced an invalid source identity")
	}
}

func TestReferenceIdentitySurvivesRenumberingAndRejectsOtherSources(t *testing.T) {
	c := testController()
	src := handoffSource()
	ref := testReference(src)
	src.ID = 999
	src.Title = "已改名"
	c.SetThreads([]model.Thread{{ID: 2, ServerID: "wrong-session", HistoryLoaded: true}, src})
	if got, _, err := c.ResolveReference(ref); err != nil || got.ServerID != src.ServerID {
		t.Fatalf("identity resolved by row number: %+v %v", got, err)
	}
	other := ref
	other.Source = "ffffffffffffffffffffffffffffffff"
	if _, _, err := c.ResolveReference(other); err == nil {
		t.Fatal("cross-source reference resolved locally")
	}
	other = ref
	other.Backend = appmodel.AgentCodex
	if _, _, err := c.ResolveReference(other); err == nil {
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
	app.C.(*controller.Controller).SetThreads([]model.Thread{a, b})
	raw := model.ReferenceText(testReference(a), a.Title) + " 对比 " + model.ReferenceText(testReference(b), b.Title)
	out, _, err := controller.ExpandThreadRefs(raw, model.ThreadRef{}, app.C.(*controller.Controller).ResolveReference)
	if err != nil {
		t.Fatal(err)
	}
	body, tags := app.ThreadReferenceContent(out)
	if body != raw || len(tags) != 2 || tags[0].Ref == tags[1].Ref {
		t.Fatalf("title merged identities: %s %+v", body, tags)
	}
	app.StartEdit(model.Message{ID: 42, Content: out})
	if app.EditText != raw {
		t.Fatal(app.EditText)
	}
}

func TestForkKeepsOriginalReferenceAndMetadataAcrossRestart(t *testing.T) {
	c := testController()
	c.ReferenceDir = t.TempDir()
	src := handoffSource()
	ref := testReference(src)
	c.SetThreads([]model.Thread{src})
	c.Update(src.ID, func(t *model.Thread) {
		t.ServerID = "forked-session"
		t.Messages = []model.Message{{Role: "user", Content: "new branch"}}
	})
	got, messages, err := c.ResolveReference(ref)
	if err != nil || got.ServerID != ref.SessionID || len(messages) != len(src.Messages) {
		t.Fatalf("reference followed fork: %+v %v", got, err)
	}
	read, err := c.MCP().Handle("mcpRead", mcpproto.Request{Reference: ref.URL()})
	if err != nil || !strings.Contains(read.(mcpproto.ReadResult).Packet, "修复登录过期的问题") {
		t.Fatalf("original session link unreadable after fork: %v %v", read, err)
	}
	restarted := testController()
	restarted.ReferenceDir = c.ReferenceDir
	restarted.LoadForkedReferences()
	if saved, ok := restarted.ForkedReferences[ref]; !ok || saved.Ref.SessionID != src.ServerID {
		t.Fatalf("original metadata lost: %+v", restarted.ForkedReferences)
	}
}

func TestInvalidReferencePreventsQueueAndPreservesDraft(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "target", Busy: true, HistoryLoaded: true}})
	c.SetActive(1)
	raw := model.ReferenceText(testRef(appmodel.AgentCodex, "missing"), "不存在")
	c.RememberDraft(raw)
	c.Send()
	if len(c.Current().Queue) != 0 || c.Draft() != raw || !strings.Contains(c.LastSubmitError, "不存在") {
		t.Fatalf("invalid reference sent: %+v %q %s", c.Current().Queue, c.Draft(), c.LastSubmitError)
	}
}

func TestReferenceSnapshotDoesNotCutLiteralContextTags(t *testing.T) {
	raw := "解释 <miya-thread-context 的含义，继续正文"
	if own, snapshots := model.SplitReferenceContext(raw); own != raw || len(snapshots) != 0 {
		t.Fatal(own)
	}
	src := handoffSource()
	ref := testReference(src)
	raw += " " + model.ReferenceText(ref, src.Title)
	out, _, err := controller.ExpandThreadRefs(raw, model.ThreadRef{}, func(model.ThreadRef) (model.Thread, []model.Message, error) { return src, src.Messages, nil })
	if err != nil {
		t.Fatal(err)
	}
	if own, snapshots := model.SplitReferenceContext(out); own != raw || len(snapshots) != 1 {
		t.Fatal(own)
	}
	withAttachment, _ := msgfmt.AttachmentDisplay(out, []string{"notes.txt"})
	if own, snapshots := model.SplitReferenceContext(withAttachment); own != raw+"\n📎 notes.txt" || len(snapshots) != 1 {
		t.Fatalf("attachment exposed or truncated context: %q", own)
	}
	quoted := "```\n" + out + "\n```"
	if own, snapshots := model.SplitReferenceContext(quoted); own != quoted || len(snapshots) != 0 {
		t.Fatal("code sample was hidden as a snapshot")
	}
	joined := model.JoinReferenceMessages([]string{out, "接着做"})
	if own, snapshots := model.SplitReferenceContext(joined); own != raw+"\n\n接着做" || len(snapshots) != 1 {
		t.Fatal(own)
	}
}
