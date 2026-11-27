package nsui

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/uiruntime"
)

func TestComposerEmptyThreadSuggestions(t *testing.T) {
	h := newFakeHost(model.Thread{})
	app := startBridged(t, h)
	for _, id := range []string{"snake", "bug", "pdf"} {
		if !app.Exists("id:suggestion-" + id) {
			t.Fatal("missing suggestion", id)
		}
	}
	app.Press("id:suggestion-bug")
	app.Settle()
	if h.draft != "Find and fix a bug in my code. Explain the root cause and verify the fix." {
		t.Fatalf("draft = %q", h.draft)
	}
	if !app.Enabled("id:composer-send") {
		t.Fatal("suggestion did not enable Send")
	}
}
func TestComposerQueueEditingAndSteering(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 7, Busy: true, Queue: []model.QueuedMessage{{ID: 11, SendRequest: model.SendRequest{Text: "next change", Attachments: []string{"/tmp/notes.md"}}}}})
	h.draft = "existing draft"
	app := startBridged(t, h)
	if !strings.Contains(app.Find("id:queue-text-11").Text, "next change") {
		t.Fatal("queue not displayed")
	}
	before := app.Find("id:queue-text-11").ID
	app.Post(func() { h.threads[0].Queue[0].Text = "updated change"; uiruntime.Invalidate() })
	app.Settle()
	if after := app.Find("id:queue-text-11"); after.ID != before || !strings.Contains(after.Text, "updated change") {
		t.Fatal("queued row did not update in place")
	}
	app.Press("id:queue-steer-11")
	app.Press("id:queue-edit-11")
	app.Settle()
	if h.draft != "updated change\nexisting draft" || len(h.attachments) != 1 || len(h.Current().Queue) != 0 {
		t.Fatalf("draft=%q attachments=%v queue=%v", h.draft, h.attachments, h.Current().Queue)
	}
	if len(h.queuedActions) != 1 || h.queuedActions[0] != "steer" {
		t.Fatal(h.queuedActions)
	}
	app.Press("id:composer-send")
	app.Settle()
	if len(h.sent) != 1 || h.sent[0] {
		t.Fatal("normal send should queue", h.sent)
	}
	app.Type("id:composer", "immediate")
	app.Press("id:steer-draft")
	app.Settle()
	if len(h.sent) != 2 || !h.sent[1] {
		t.Fatal("steer action missing", h.sent)
	}
}
func TestComposerPausedQueue(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 7, StopRequested: true, Queue: []model.QueuedMessage{{ID: 1, SendRequest: model.SendRequest{Text: "later"}}}})
	app := startBridged(t, h)
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "Sending paused") {
		t.Fatal("paused queue missing")
	}
	app.Press("id:resume-queue")
	app.Settle()
	if len(h.queuedActions) != 1 {
		t.Fatal("resume not called")
	}
}
func TestComposerEditsSentPrompt(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 9, HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "user", Content: "Original prompt\n📎 /tmp/file.txt", TurnID: "t1"}}})
	h.canRewind = true
	app := startBridged(t, h)
	app.Press("id:edit-message-1")
	app.Settle()
	if got := app.Find("id:edit-message-input-1").Text; got != "Original prompt" {
		t.Fatal(got)
	}
	app.Type("id:edit-message-input-1", " revised")
	app.Press("id:edit-cancel-1")
	app.Settle()
	if h.rewound.messageID != 0 {
		t.Fatal("cancel rewound conversation")
	}
	app.Press("id:edit-message-1")
	app.Type("id:edit-message-input-1", " revised")
	app.Press("id:edit-send-1")
	app.Settle()
	if h.rewound.threadID != 9 || h.rewound.messageID != 1 || h.rewound.text != "Original prompt revised" {
		t.Fatalf("rewind=%+v", h.rewound)
	}
}
func TestComposerAttachmentChipsAndPureAttachmentSend(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 5})
	h.attachments = []string{"/tmp/notes.md", "/tmp/screen.png"}
	app := startBridged(t, h)
	if !app.Exists("id:attachment-/tmp/notes.md") || !app.Exists("id:attachment-/tmp/screen.png") {
		t.Fatal("attachment chips missing")
	}
	app.Press("id:remove-attachment-/tmp/notes.md")
	app.Settle()
	if len(h.attachments) != 1 || h.attachments[0] != "/tmp/screen.png" {
		t.Fatal(h.attachments)
	}
	if !app.Enabled("id:composer-send") {
		t.Fatal("attachment-only send disabled")
	}
	app.Press("id:composer-send")
	app.Settle()
	if len(h.sent) != 1 {
		t.Fatal("attachment not sent")
	}
	app.DropFiles("id:composer-card", "file:///tmp/a%20b.txt")
	app.Settle()
	if len(h.attachments) != 1 || h.attachments[0] != "/tmp/a b.txt" {
		t.Fatal(h.attachments)
	}
	app.Paste("id:composer", core.PasteEvent{Files: []core.PastedFile{{Name: "clipboard.png", MIME: "image/png", Data: []byte("png data")}}})
	app.WaitUntil(time.Second, func() bool { return len(h.attachments) == 2 })
	p := h.attachments[1]
	t.Cleanup(func() { _ = os.RemoveAll(strings.TrimSuffix(p, "/clipboard.png")) })
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "png data" {
		t.Fatalf("staged paste %q %v", data, err)
	}
}
func TestComposerHandoffAndReferenceCompletion(t *testing.T) {
	ref := model.ThreadRef{Source: strings.Repeat("a", 32), Backend: model.AgentCodex, SessionID: "session-1"}
	h := newFakeHost(model.Thread{ID: 8, Title: "Source", ServerID: ref.SessionID, HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "assistant", Content: "Finished", Status: "completed", TurnID: "t1"}}})
	h.refs = map[int]model.ThreadRef{8: ref}
	h.state.ReferenceSource = ref.Source
	app := startBridged(t, h)
	app.Press("id:handoff")
	app.Settle()
	if h.current != 0 || h.draft != model.ReferenceText(ref, "Source")+" " || len(h.prefetched) != 1 {
		t.Fatalf("handoff: current=%d draft=%q prefetch=%v", h.current, h.draft, h.prefetched)
	}
	if !app.Exists("text:@Source") {
		t.Fatal("reference chip missing")
	}
	app.Post(func() { h.RememberDraft("") })
	app.Settle()
	app.Type("id:composer", "@Sou")
	app.Settle()
	if !app.Exists("id:completion-" + ref.URL()) {
		t.Fatal("thread completion missing")
	}
	app.Press("id:completion-" + ref.URL())
	app.Settle()
	if h.draft != model.ReferenceText(ref, "Source")+" " || len(h.prefetched) != 2 {
		t.Fatal("completion lost identity", h.draft, h.prefetched)
	}
}
func TestComposerReadOnlyAndReconnect(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, ReadOnly: true})
	h.controls.problem = "Agent disconnected"
	h.controls.reconnect = true
	app := startBridged(t, h)
	if app.Exists("id:composer") || !app.Exists("id:readonly-composer") {
		t.Fatal("read-only composer not replaced")
	}
	app.Press("id:reconnect-agent")
	app.Settle()
	if h.controls.problem != "" {
		t.Fatal("reconnect not called")
	}
}

// Type inference lets the fixture answer the driver's file request without
// importing NSGo's private driver package.
func pickedFiles[T any](_ func(T) []core.File, files []core.File) func(T) []core.File {
	return func(T) []core.File { return files }
}
func TestComposerFilePickerAndPickers(t *testing.T) {
	h := newFakeHost(model.Thread{})
	app := startBridged(t, h)
	app.PickFiles = pickedFiles(app.PickFiles, []core.File{{Path: "/tmp/chosen.txt"}})
	app.Press("id:attach-files")
	app.WaitUntil(time.Second, func() bool { return len(h.attachments) == 1 })
	if h.attachments[0] != "/tmp/chosen.txt" || len(app.FilePicks) != 1 || app.FilePicks[0].Limit < 2 || app.FilePicks[0].Title != tr("Add files") || app.FilePicks[0].Start != h.state.Cwd || app.FilePicks[0].Button != tr("Attach") {
		t.Fatal("file picker did not allow multiple files", h.attachments)
	}
	for _, p := range []struct{ id, choice string }{{"model", "fast"}, {"effort", "high"}, {"permissions", "never"}, {"agent", model.AgentClaude}} {
		app.Press("id:picker-" + p.id)
		app.Settle()
		app.Press("id:pick-" + p.id + "-" + p.choice)
		app.Settle()
	}
	if h.controls.model != "fast" || h.controls.effort != "high" || h.permission != "never" || h.CurrentAgent() != model.AgentClaude {
		t.Fatalf("picker state %+v %s", h.controls, h.permission)
	}
}

func TestComposerAttachmentPreparationDoesNotCrossThreads(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1}, model.Thread{ID: 2})
	var d *desktopComposer
	t.Cleanup(func() { uiruntime.SetAppWindow(nil); current = nil })
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node { installBridge(cx); d = newDesktopComposer(cx, h); return core.View{} })
	gate := make(chan struct{})
	app.Post(func() {
		d.stageAttachments(h.state.Cwd, 1, h.state.ReferenceSource, func() ([]string, error) { <-gate; return []string{"/tmp/original.txt"}, nil })
		h.SelectThread(h.threads[1])
	})
	close(gate)
	app.WaitUntil(time.Second, func() bool { return d.preparing.GetUntracked() == 0 })
	if len(h.attachments) > 0 {
		t.Fatal("attachment preparation crossed into the next conversation", h.attachments)
	}
}
func TestComposerCollaborationRowsAndRetry(t *testing.T) {
	ref := model.ThreadRef{Source: strings.Repeat("c", 32), Backend: model.AgentCodex, SessionID: "parent"}
	h := newFakeHost(model.Thread{ID: 1, ServerID: "parent"}, model.Thread{ID: 2, ServerID: "other"})
	h.refs = map[int]model.ThreadRef{1: ref, 2: {Source: ref.Source, Backend: ref.Backend, SessionID: "other"}}
	h.state.ReferenceSource = ref.Source
	h.collab = &model.CollaborationStatus{Version: 1, Revision: 1}
	progress := "first step"
	var requests []appmodel.CollaborationRequest
	h.collaborationCall = func(r appmodel.CollaborationRequest) (any, error) {
		switch r.Operation {
		case "list_tasks":
			return map[string]any{"revision": h.collab.Revision, "tasks": []map[string]any{{"task_id": "child", "title": "Delegate work", "state": "running", "progress": progress}}}, nil
		case "read_task":
			return map[string]any{"task_id": "child", "title": "Delegate work", "state": "running", "progress": progress}, nil
		case "cancel_task":
			requests = append(requests, r)
			if len(requests) == 1 {
				return nil, errors.New("temporary transport failure")
			}
			return map[string]any{"accepted": true}, nil
		}
		return nil, errors.New("unexpected " + r.Operation)
	}
	app := startBridged(t, h)
	app.WaitFor("id:collaboration-toggle", time.Second)
	app.Press("id:collaboration-toggle")
	app.Settle()
	before := app.Find("id:collaboration-task-child").ID
	app.Post(func() { progress = "second step"; h.collab.Revision++; uiruntime.Invalidate() })
	app.Settle()
	if got := app.Find("id:collaboration-task-child"); got.ID != before {
		t.Fatal("collaboration poll replaced a keyed row")
	}
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "second step") {
		t.Fatal("task progress did not refresh")
	}
	app.Press("id:collaboration-task-child")
	app.Settle()
	app.Press("id:collaboration-cancel")
	app.Settle()
	// An ambiguous request survives navigation and still retries the same ID.
	app.Post(func() { h.SelectThread(h.threads[1]) })
	app.Settle()
	app.Post(func() { h.SelectThread(h.threads[0]) })
	app.Settle()
	app.Press("id:collaboration-toggle")
	app.Settle()
	app.Press("id:collaboration-retry")
	app.Settle()
	if len(requests) != 2 || string(requests[0].Arguments) != string(requests[1].Arguments) {
		t.Fatalf("retry changed request identity: %+v", requests)
	}
	var args map[string]any
	if err := json.Unmarshal(requests[0].Arguments, &args); err != nil || args["request_id"] == "" {
		t.Fatal("missing operation request ID", args, err)
	}
}

type mirroredComposerHost struct{ *fakeHost }

func (mirroredComposerHost) RemoteHost() bool { return true }
func TestComposerMirroredDraftPreviewsLocalAttachment(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1})
	h.attachments = []string{"/tmp/local image.png"}
	app := startBridged(t, mirroredComposerHost{h})
	if got := app.Find("id:attachment-image-/tmp/local image.png").Image.Source.URI; got != "file:///tmp/local%20image.png" {
		t.Fatalf("new local attachment used server preview: %q", got)
	}
}

type commandComposerHost struct {
	*fakeHost
	commands []string
}

func (h *commandComposerHost) RunThreadCommand(command string) string {
	h.commands = append(h.commands, command)
	return ""
}
func TestComposerCompletionKeysDoNotSendOrEdit(t *testing.T) {
	h := &commandComposerHost{fakeHost: newFakeHost(model.Thread{ID: 1})}
	h.collab = &model.CollaborationStatus{Version: 1}
	app := startBridged(t, h)
	app.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	app.Type("id:composer", "/rev")
	if !app.Exists("id:composer-completion") {
		t.Fatal("completion did not open")
	}
	app.Key("down")
	app.Key("up")
	app.Key("tab")
	if h.draft != "/review " || len(h.sent) != 0 || app.Exists("id:composer-completion") {
		t.Fatalf("draft=%q sends=%v", h.draft, h.sent)
	}
	app.Find("text:/review")
	app.Key("enter")
	if len(h.commands) != 1 || h.commands[0] != "review" {
		t.Fatal("Enter after completion did not execute command", h.commands)
	}
	app.Post(func() { h.RememberDraft("") })
	app.Settle()
	app.Type("id:composer", "/rev")
	app.Key("escape")
	if h.draft != "/rev" || app.Exists("id:composer-completion") {
		t.Fatal("Escape changed draft or kept popup", h.draft)
	}
	app.Post(func() { h.RememberDraft("plain message") })
	app.Settle()
	app.Key("enter")
	if len(h.sent) != 1 {
		t.Fatal("Enter after Escape did not send", h.sent)
	}
	app.Type("id:composer", "/rev")
	app.Key("enter")
	if h.draft != "/review " || len(h.sent) != 1 {
		t.Fatal("completion Enter sent", h.draft, h.sent)
	}
	app.Post(func() { h.RememberDraft("") })
	app.Settle()
	app.Type("id:composer", "#Claude")
	app.Key("enter")
	mentions := model.AgentMentions(h.draft)
	if len(mentions) != 1 || mentions[0].Agent != model.AgentClaude || len(h.sent) != 1 {
		t.Fatal("agent completion lost identity or sent", h.draft, h.sent)
	}
	app.Find("text:#Claude")

}

func TestComposerAttachmentOnlyEnterAndLoading(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1})
	h.attachments = []string{"/tmp/attachment.png"}
	app := startBridged(t, h)
	app.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	app.Focus("id:composer")
	app.Key("enter")
	if len(h.sent) != 1 {
		t.Fatal("attachment-only Enter did not send", h.sent)
	}
	app.Post(func() { h.threads[0].Loading = true; uiruntime.Invalidate() })
	app.Settle()
	if app.Enabled("id:composer") {
		t.Fatal("loading editor enabled")
	}
	app.DropFiles("id:composer-card", "file:///tmp/ignored")
	if h.draft != "" || len(h.attachments) != 0 {
		t.Fatal("loading editor accepted data", h.draft, h.attachments)
	}
}
