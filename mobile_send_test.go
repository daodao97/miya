package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMobileNewSendStaysVisibleUntilHistoryArrives(t *testing.T) {
	for _, agent := range []string{agentCodex, agentClaude} {
		for _, width := range []int{320, 390} {
			t.Run(fmt.Sprintf("%s/%d", agent, width), func(t *testing.T) {
				m := testMobileApp()
				m.newProject, m.newAgent = "/a", agent
				m.router().Push("/thread")
				m.sending, m.draft = true, "请修复手机新建对话的空白问题"
				m.beginNewSend(m.draft, nil)
				tt := ui.NewTester(m.viewRoot, width, 750)
				tt.Frame()
				if !tt.HasText(m.draft) || !tt.HasText("发送中…") || tt.HasText("Let’s build") {
					t.Fatalf("first message is not immediately visible: %v", tt.Texts())
				}
				if len(m.view.c.current().Messages) != 0 || len(m.msgCache) != 0 {
					t.Fatal("the local preview entered synchronized history")
				}
				m.sent(3, "")
				tt.Frame()
				if !tt.HasText("请修复手机新建对话的空白问题") || !tt.HasText("正在同步对话…") || tt.HasText("Loading conversation…") {
					t.Fatalf("send acknowledgement replaced the prompt with an empty list: %v", tt.Texts())
				}
				if len(m.subscribeRequest(3).Have) != 0 {
					t.Fatal("the phone advertised an unconfirmed local message")
				}
				m.receiveThread(3, phoneThreadDelta{Thread: Thread{ID: 3, Agent: agent, Loading: true}, Reset: true})
				tt.Frame()
				if !tt.HasText("请修复手机新建对话的空白问题") {
					t.Fatal("a loading delta hid the local message")
				}
				m.receiveThread(3, phoneThreadDelta{Thread: Thread{ID: 3, ServerID: "created", Agent: agent, HistoryLoaded: true, Busy: true}, Reset: true,
					Order: []int{10}, Changed: []Message{{ID: 10, Role: "user", Content: "请修复手机新建对话的空白问题"}}})
				tt.Frame()
				if m.newSend != nil || !tt.HasText("请修复手机新建对话的空白问题") || tt.HasText("正在同步对话…") {
					t.Fatalf("authoritative history did not replace the preview: %v", tt.Texts())
				}
				messages := m.view.c.current().Messages
				if len(messages) != 1 || messages[0].ID != 10 || len(m.subscribeRequest(3).Have) != 1 {
					t.Fatal("the first message was duplicated or lost during synchronization")
				}
			})
		}
	}
}

func TestMobileNewSendFailureKeepsDraftAndPhotos(t *testing.T) {
	m := testMobileApp()
	m.newProject, m.newAgent = "/a", agentCodex
	m.router().Push("/thread")
	photo := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(photo, []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	m.draft, m.images, m.sending = "保留这条消息", []string{photo}, true
	m.beginNewSend(m.draft, m.images)
	m.sent(0, "桌面端还没准备好，请稍后再试")
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if m.newSend != nil || m.sending || m.draft != "保留这条消息" || len(m.images) != 1 || !tt.HasText(m.sendErr) {
		t.Fatal("failed send did not restore the editable draft and show the error")
	}
	if _, err := os.Stat(photo); err != nil {
		t.Fatal("failed send deleted the photo needed for retry")
	}
	m.beginNewSend("修改后重试", nil)
	shown, ok := m.newSendThread(m.view.c.current())
	if !ok || shown.Messages[0].Content != "修改后重试" {
		t.Fatal("retry reused the earlier message preview")
	}
	m.home()
	if m.newSend != nil {
		t.Fatal("leaving the conversation kept its pending preview")
	}
}

func TestMobileNewSendRetainsPhotosAfterAcknowledgement(t *testing.T) {
	m := testMobileApp()
	m.newProject = "/a"
	photo := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(photo, []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	m.images, m.sending = []string{photo}, true
	m.beginNewSend("", m.images)
	m.sent(3, "")
	if len(m.images) != 0 || m.newSend == nil || len(m.newSend.message.Images) != 1 {
		t.Fatal("acknowledgement did not transfer photos from the composer to the preview")
	}
	if _, err := os.Stat(photo); err != nil {
		t.Fatal("acknowledgement removed the photo before the desktop history arrived")
	}
	m.openThread(Thread{ID: 4, ServerID: "other"})
	if m.newSend != nil {
		t.Fatal("switching conversations displayed an unrelated pending message")
	}
}
