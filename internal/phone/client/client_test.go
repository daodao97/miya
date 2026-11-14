package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"miya/internal/chat/model"
	"miya/internal/notification"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

func threadClient() *Client {
	m := New()
	m.Link = "desktop"
	m.Open = 7
	m.Path = "/thread"
	th := model.Thread{ID: 7, Agent: "codex", ServerID: "stable", HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "user", Content: "old"}}}
	m.State = phoneproto.State{ReferenceSource: "desktop", Projects: []model.ProjectSnapshot{{Name: "work", Path: "/work", Current: true, Threads: []model.Thread{th}}}}
	m.ShowThread(th)
	return m
}
func TestPendingSendDoesNotEnterHistoryOrDiskCache(t *testing.T) {
	m := threadClient()
	m.DataDir = t.TempDir()
	m.OpenDiskCache(m.Link)
	defer m.Close()
	m.Remember("stable", m.Viewer.Current().Messages)
	m.Draft = "local only"
	m.BeginSend(m.ThreadRequest("send", 7), nil)
	shown, ok := m.PendingSendThread(m.Viewer.Current())
	if !ok || len(shown.Messages) != 2 {
		t.Fatal("no local projection")
	}
	m.FlushCache(true)
	if got := m.loadDisk("stable"); len(got) != 1 || got[0].Content != "old" {
		t.Fatalf("pending message entered cache: %#v", got)
	}
	if len(m.SubscribeRequest(7).Have) != 1 || len(m.Viewer.Current().Messages) != 1 {
		t.Fatal("pending message entered authoritative history")
	}
}
func TestDeltaRebindAndLoadingKeepCachedHistory(t *testing.T) {
	m := threadClient()
	m.Remember("stable", m.Viewer.Current().Messages)
	th := m.Viewer.Current()
	th.ID = 42
	m.ApplyPhoneState(phoneproto.State{Projects: []model.ProjectSnapshot{{Path: "/work", Threads: []model.Thread{th}}}})
	if m.Open != 42 || m.Viewer.Current().ServerID != "stable" {
		t.Fatal("numeric ID replaced durable identity")
	}
	m.ReceiveThread(42, phoneproto.ThreadDelta{Thread: model.Thread{ID: 42, ServerID: "stable", Agent: "codex", Loading: true}})
	if len(m.Viewer.Current().Messages) != 1 {
		t.Fatal("loading response cleared history")
	}
	m.ReceiveThread(42, phoneproto.ThreadDelta{Thread: model.Thread{ID: 42, ServerID: "someone-else", Agent: "codex"}, Reset: true, Order: []int{2}, Changed: []model.Message{{ID: 2, Role: "user", Content: "wrong"}}})
	if m.Viewer.Current().Messages[0].Content != "old" {
		t.Fatal("reused runtime ID crossed sessions")
	}
}
func TestQueueEchoAndRepeatedPromptDoNotDuplicatePending(t *testing.T) {
	m := threadClient()
	r := m.ThreadRequest("send", 7)
	r.Text = "again"
	m.BeginSend(r, nil)
	m.Sent(7, "")
	th := m.Viewer.Current()
	th.Queue = []model.QueuedMessage{{ID: 10, Text: "again"}}
	m.ShowThread(th)
	m.receivePendingSends(th)
	if len(m.Outbox) != 0 {
		t.Fatal("queue echo did not retire local preview")
	}
	m.BeginSend(r, nil)
	m.Sent(7, "")
	m.receivePendingSends(th)
	if len(m.Outbox) != 1 {
		t.Fatal("existing queue entry acknowledged a later identical send")
	}
	th.Queue = append(th.Queue, model.QueuedMessage{ID: 11, Text: "again"})
	m.ShowThread(th)
	m.receivePendingSends(th)
	if len(m.Outbox) != 0 {
		t.Fatal("new queue entry did not acknowledge later send")
	}
}
func TestFailedUploadRetainsDraftAndCanBeEdited(t *testing.T) {
	m := threadClient()
	posts := make(chan func(), 3)
	m.Dispatch = func(f func()) { posts <- f }
	m.SendRequest = func(remote.Request) error { return nil }
	missing := filepath.Join(t.TempDir(), "missing.png")
	m.Images = []string{missing}
	m.Draft = "upload"
	m.Send()
	m.Draft = "next"
	select {
	case f := <-posts:
		f()
	case <-time.After(time.Second):
		t.Fatal("no upload failure")
	}
	if m.Sending || m.Draft != "next" || len(m.Outbox) != 1 || m.Pending()[0].Failure == "" {
		t.Fatal("upload failure lost ownership")
	}
	m.EditPending(m.Outbox[0].Message.ID)
	if m.Draft != "next" {
		t.Fatal("edit overwrote a newer draft")
	}
	m.Draft = ""
	m.EditPending(m.Outbox[0].Message.ID)
	if m.Draft != "upload" || len(m.Images) != 1 || len(m.Outbox) != 0 {
		t.Fatal("edit did not return photo ownership")
	}
}
func TestCompletionReadRequiresLiveContents(t *testing.T) {
	m := threadClient()
	th := m.Viewer.Current()
	th.CompletedAtMs = 100
	m.ShowThread(th)
	if _, _, ok := m.CompletionReadRequest(); ok {
		t.Fatal("cached history acknowledged completion")
	}
	m.ReceiveThread(7, phoneproto.ThreadDelta{Thread: th, Order: []int{1}})
	r, _, ok := m.CompletionReadRequest()
	if !ok || r.ReadCompletedAtMs != 100 || r.ServerID != "stable" {
		t.Fatalf("live read: %#v %v", r, ok)
	}
	m.ApplyNotifications(notification.PhoneState{Revision: 2, Unread: []notification.Unread{}})
	m.ApplyNotifications(notification.PhoneState{Revision: 1, Unread: []notification.Unread{{Key: "stale"}}})
	if len(m.State.Notifications.Unread) != 0 {
		t.Fatal("stale unread state restored an old badge")
	}
}
func TestNetworkChangeWakesReconnectAndForegroundPreservesDraft(t *testing.T) {
	m := threadClient()
	m.Cancel = func() {}
	m.Wake = make(chan struct{}, 1)
	m.NetworkPathChanged(false, "")
	m.NetworkPathChanged(true, "wifi")
	select {
	case <-m.Wake:
	default:
		t.Fatal("new path did not retry")
	}
	m.Draft = "keep"
	m.EnterBackground()
	if m.Cancel != nil || m.Draft != "keep" {
		t.Fatal("background lost draft or kept socket")
	}
}
func TestFileTargetStaysInRegisteredProjects(t *testing.T) {
	m := threadClient()
	project, file, ok := m.FileTarget("src/a.go")
	if !ok || project != "/work" || file != "src/a.go" {
		t.Fatal("relative file did not resolve")
	}
	if _, _, ok = m.FileTarget("../../etc/passwd"); ok {
		t.Fatal("file escaped registered projects")
	}
}
func TestPersistedOptionsAreDesktopScoped(t *testing.T) {
	m := New()
	m.DataDir = t.TempDir()
	m.OpenDiskCache("desktop-a")
	m.SetOptions("codex", remote.SendOptions{Model: "a", Policy: "never"})
	m.OpenDiskCache("desktop-b")
	m.loadDesktopOptions()
	if m.SendOptions("codex").Model == "a" {
		t.Fatal("options crossed desktops")
	}
	m.OpenDiskCache("desktop-a")
	m.loadDesktopOptions()
	if m.SendOptions("codex").Model != "a" {
		t.Fatal("options not restored")
	}
}
func TestCancelledNetworkPreparationDoesNotDial(t *testing.T) {
	m := New()
	posts := make(chan func(), 5)
	m.Dispatch = func(f func()) { posts <- f }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.PrepareNetwork = func(ctx context.Context, _ string) error { cancel(); return ctx.Err() }
	m.DialDesktop = func(context.Context, string) (*remote.Conn, error) {
		t.Error("dialed after denied preparation")
		return nil, errors.New("unexpected")
	}
	m.Run(ctx, m.Generation, "unused", nil)
}
func TestPreviewRejectsInvalidBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "preview.jpg")
	if StorePhonePreview(p, []byte("bad")) == nil {
		t.Fatal("invalid image accepted")
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid preview written")
	}
}
