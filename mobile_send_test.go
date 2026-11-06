package main

import (
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	os "os"
	filepath "path/filepath"
	testing "testing"

	ui "github.com/egoist/mygo/ui"
)

func TestMobileNewSendStaysVisibleUntilHistoryArrives(t *testing.T) {
	for _, agent := range []string{appmodel.AgentCodex, appmodel.AgentClaude} {
		for _, width := range []int{320, 390} {
			t.Run(fmt.Sprintf("%s/%d", agent, width), func(t *testing.T) {
				m := testMobileApp()
				m.NewProject, m.NewAgent = "/a", agent
				m.Router().Push("/thread")
				m.Sending, m.Draft = true, "请修复手机新建对话的空白问题"
				m.BeginNewSend(m.Draft, nil)
				tt := ui.NewTester(m.ViewRoot, width, 750)
				tt.Frame()
				if !tt.HasText(m.Draft) || !tt.HasText("Sending…") || tt.HasText("Let’s build") {
					t.Fatalf("first message is not immediately visible: %v", tt.Texts())
				}
				if len(m.Viewer.Current().Messages) != 0 || len(m.MsgCache) != 0 {
					t.Fatal("the local preview entered synchronized history")
				}
				m.Sent(3, "")
				tt.Frame()
				if !tt.HasText("请修复手机新建对话的空白问题") || !tt.HasText("Syncing conversation…") || tt.HasText("Loading conversation…") {
					t.Fatalf("send acknowledgement replaced the prompt with an empty list: %v", tt.Texts())
				}
				if len(m.SubscribeRequest(3).Have) != 0 {
					t.Fatal("the phone advertised an unconfirmed local message")
				}
				m.ReceiveThread(3, appmodel.PhoneThreadDelta{Thread: appmodel.Thread{ID: 3, Agent: agent, Loading: true}, Reset: true})
				tt.Frame()
				if !tt.HasText("请修复手机新建对话的空白问题") {
					t.Fatal("a loading delta hid the local message")
				}
				m.ReceiveThread(3, appmodel.PhoneThreadDelta{Thread: appmodel.Thread{ID: 3, ServerID: "created", Agent: agent, HistoryLoaded: true, Busy: true}, Reset: true,
					Order: []int{10}, Changed: []appmodel.Message{{ID: 10, Role: "user", Content: "请修复手机新建对话的空白问题"}}})
				tt.Frame()
				if m.NewSend != nil || !tt.HasText("请修复手机新建对话的空白问题") || tt.HasText("Syncing conversation…") {
					t.Fatalf("authoritative history did not replace the preview: %v", tt.Texts())
				}
				messages := m.Viewer.Current().Messages
				if len(messages) != 1 || messages[0].ID != 10 || len(m.SubscribeRequest(3).Have) != 1 {
					t.Fatal("the first message was duplicated or lost during synchronization")
				}
			})
		}
	}
}

func TestMobileNewSendKeepsMessageWhenServerIdentityArrives(t *testing.T) {
	m := testMobileApp()
	m.NewProject, m.NewAgent = "/a", appmodel.AgentCodex
	m.Router().Push("/thread")
	m.Sending, m.Draft = true, "首条消息应一直可见"
	m.BeginNewSend(m.Draft, nil)
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	m.Sent(3, "")
	// Codex publishes the local user message before thread/start returns
	// its durable ID. The phone has already subscribed by the numeric ID.
	sync := appmodel.NewPhoneThreadSync(nil)
	thread := appmodel.Thread{ID: 3, Agent: appmodel.AgentCodex, HistoryLoaded: true, Busy: true,
		Messages: []appmodel.Message{{ID: 10, Role: "user", Content: "首条消息应一直可见", ItemID: "pending-user"}}}
	delta, changed := sync.Update(thread)
	if !changed {
		t.Fatal("initial user message was not published")
	}
	m.ReceiveThread(3, delta)
	tt.Frame()
	if m.NewSend != nil || !tt.HasText("首条消息应一直可见") {
		t.Fatal("initial desktop message did not replace the local preview")
	}
	// Only metadata changes next; the desktop correctly omits the user
	// message because its fingerprint was already sent.
	thread.ServerID = "created"
	delta, changed = sync.Update(thread)
	if !changed || len(delta.Changed) != 0 || delta.Reset {
		t.Fatalf("expected an identity-only delta: %+v", delta)
	}
	m.ReceiveThread(3, delta)
	tt.Frame()
	if !tt.HasText("首条消息应一直可见") || len(m.Viewer.Current().Messages) != 1 {
		t.Fatalf("assigning the durable ID hid the first message: %v", tt.Texts())
	}
	if got := m.SubscribeRequest(3); got.ServerID != "created" || len(got.Have) != 1 || got.Have[0].ID != 10 {
		t.Fatalf("confirmed history was not retained for reconnection: %+v", got)
	}
	thread.Messages = append(thread.Messages, appmodel.Message{ID: 11, Role: "assistant", Content: "正在处理"})
	delta, _ = sync.Update(thread)
	m.ReceiveThread(3, delta)
	tt.Frame()
	messages := m.Viewer.Current().Messages
	if !tt.HasText("首条消息应一直可见") || len(messages) != 2 || messages[1].Content != "正在处理" {
		t.Fatal("subsequent streaming lost or duplicated the first message")
	}
}

func TestMobileNewSendFailureKeepsDraftAndPhotos(t *testing.T) {
	m := testMobileApp()
	m.NewProject, m.NewAgent = "/a", appmodel.AgentCodex
	m.Router().Push("/thread")
	photo := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(photo, []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Draft, m.Images, m.Sending = "保留这条消息", []string{photo}, true
	m.BeginNewSend(m.Draft, m.Images)
	m.Sent(0, "Desktop is not ready. Try again shortly.")
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if m.NewSend != nil || m.Sending || m.Draft != "保留这条消息" || len(m.Images) != 1 || !tt.HasText(m.SendErr) {
		t.Fatal("failed send did not restore the editable draft and show the error")
	}
	if _, err := os.Stat(photo); err != nil {
		t.Fatal("failed send deleted the photo needed for retry")
	}
	m.BeginNewSend("修改后重试", nil)
	shown, ok := m.NewSendThread(m.Viewer.Current())
	if !ok || shown.Messages[0].Content != "修改后重试" {
		t.Fatal("retry reused the earlier message preview")
	}
	m.Home()
	if m.NewSend != nil {
		t.Fatal("leaving the conversation kept its pending preview")
	}
}

func TestMobileNewSendRetainsPhotosAfterAcknowledgement(t *testing.T) {
	m := testMobileApp()
	m.NewProject = "/a"
	photo := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(photo, []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Images, m.Sending = []string{photo}, true
	m.BeginNewSend("", m.Images)
	m.Sent(3, "")
	if len(m.Images) != 0 || m.NewSend == nil || len(m.NewSend.Message.Images) != 1 {
		t.Fatal("acknowledgement did not transfer photos from the composer to the preview")
	}
	if _, err := os.Stat(photo); err != nil {
		t.Fatal("acknowledgement removed the photo before the desktop history arrived")
	}
	m.OpenThread(appmodel.Thread{ID: 4, ServerID: "other"})
	if m.NewSend != nil {
		t.Fatal("switching conversations displayed an unrelated pending message")
	}
}
