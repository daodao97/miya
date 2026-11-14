package integration

import (
	"fmt"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	mobile "miya/internal/phone/client"
	phoneproto "miya/internal/phone/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestMobileNewSendRetainsPhotosAfterAcknowledgement(t *testing.T) {
	m := testPhoneClient()
	m.NewProject = "/a"
	photo := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(photo, []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Images, m.Sending = []string{photo}, true
	beginMobileSend(m, "", m.Images)
	m.Sent(3, "")
	if len(m.Images) != 0 || len(m.Outbox) == 0 || len(m.Outbox[0].Message.Images) != 1 {
		t.Fatal("acknowledgement did not transfer photos from the composer to the preview")
	}
	if _, err := os.Stat(photo); err != nil {
		t.Fatal("acknowledgement removed the photo before the desktop history arrived")
	}
	m.OpenThread(model.Thread{ID: 4, ServerID: "other"})
	if _, ok := m.PendingSendThread(m.Viewer.Current()); ok {
		t.Fatal("switching conversations displayed an unrelated pending message")
	}
}

func beginMobileSend(m *mobile.Client, text string, images []string) {
	r := m.ThreadRequest("send", m.Open)
	r.Text = text
	if m.Open == 0 {
		r.Project, r.Agent = m.NewProject, m.NewAgent
	}
	m.BeginSend(r, images)
}

func TestMobileIdenticalSendsRequireSeparateDesktopEchoes(t *testing.T) {
	m := testPhoneClient()
	th := m.State.Projects[0].Threads[0]
	th.HistoryLoaded = true
	m.OpenThread(th)
	m.ShowThread(th)
	for range 2 {
		beginMobileSend(m, "继续", nil)
		m.Sent(th.ID, "")
	}
	th.Messages = []model.Message{{ID: 10, Role: "user", Content: "继续"}}
	delta := phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{10}, Changed: th.Messages}
	m.ReceiveThread(th.ID, delta)
	if len(m.Outbox) != 1 {
		t.Fatal("one echo confirmed two identical sends")
	}
	m.ReceiveThread(th.ID, delta)
	if len(m.Outbox) != 1 {
		t.Fatal("repeated history consumed the second send")
	}
	th.Messages[0].ID = 100
	m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{100}, Changed: th.Messages})
	if len(m.Outbox) != 1 {
		t.Fatal("reloaded first echo consumed the second send")
	}
	th.Messages = append(th.Messages, model.Message{ID: 11, Role: "user", Content: "继续"})
	m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{100, 11}, Changed: th.Messages})
	if len(m.Outbox) != 0 {
		t.Fatal("second echo did not confirm the second send")
	}
}

func TestMobileQueuedSendAndEarlyEchoDoNotDuplicate(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(fmt.Sprint(queued), func(t *testing.T) {
			m := testPhoneClient()
			th := m.State.Projects[0].Threads[0]
			th.HistoryLoaded = true
			m.OpenThread(th)
			m.ShowThread(th)
			beginMobileSend(m, "下一步", nil)
			delta := phoneproto.ThreadDelta{Thread: th, Reset: true}
			if queued {
				delta.Thread.Queue = []model.QueuedMessage{{ID: 4, SendRequest: model.SendRequest{Text: "下一步"}}}
			} else {
				delta.Order = []int{4}
				delta.Changed = []model.Message{{ID: 4, Role: "user", Content: "下一步"}}
			}
			m.ReceiveThread(th.ID, delta)
			if _, ok := m.PendingSendThread(m.Viewer.Current()); ok {
				t.Fatal("echo preceding acknowledgement duplicated the message")
			}
			m.Sent(th.ID, "")
			if len(m.Outbox) != 0 || m.Sending {
				t.Fatal("acknowledgement did not release the confirmed preview")
			}
		})
	}
}

func TestMobileSendAcknowledgementDoesNotNavigateOrClearAnotherConversation(t *testing.T) {
	m := testPhoneClient()
	m.NewProject, m.NewAgent = "/a", appmodel.AgentCodex
	m.Path = "/thread"
	beginMobileSend(m, "发到新对话", nil)
	other := m.State.Projects[1].Threads[0]
	m.OpenThread(other)
	m.Draft = "另一个对话的草稿"
	m.Sent(3, "")
	if m.Open != other.ID || m.Draft != "另一个对话的草稿" || m.Sending {
		t.Fatal("late acknowledgement changed the current conversation")
	}
	if _, ok := m.PendingSendThread(m.Viewer.Current()); ok {
		t.Fatal("preview leaked across conversations")
	}
}

func TestMobilePendingSendReusesHistoryBetweenFrames(t *testing.T) {
	m := testPhoneClient()
	th := longThread(1000)
	m.OpenThread(th)
	m.ShowThread(th)
	beginMobileSend(m, "继续处理", nil)
	first, ok := m.PendingSendThread(m.Viewer.Current())
	if !ok {
		t.Fatal("missing pending projection")
	}
	for range 10 {
		next, _ := m.PendingSendThread(m.Viewer.Current())
		if &first.Messages[0] != &next.Messages[0] {
			t.Fatal("sending animation rebuilt unchanged history")
		}
	}
	th.Messages = append(th.Messages, model.Message{ID: 10001, Role: "assistant", Content: "更新后的输出"})
	m.ShowThread(th)
	next, _ := m.PendingSendThread(m.Viewer.Current())
	if len(next.Messages) != len(th.Messages)+1 || next.Messages[len(next.Messages)-2].Content != "更新后的输出" {
		t.Fatal("projection cache hid new desktop output")
	}
}

func TestMobileSendDoesNotMistakeReloadedHistoryForDelivery(t *testing.T) {
	m := testPhoneClient()
	th := m.State.Projects[0].Threads[0]
	th.HistoryLoaded = true
	th.Messages = []model.Message{{ID: 1, Role: "user", Content: "继续"}}
	m.OpenThread(th)
	m.ShowThread(th)
	beginMobileSend(m, "继续", nil)
	m.Sent(th.ID, "")
	th.Messages[0].ID = 100
	m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{100}, Changed: th.Messages})
	if len(m.Outbox) != 1 {
		t.Fatal("reassigned old history IDs hid the newly submitted message")
	}
	th.Messages = append(th.Messages, model.Message{ID: 101, Role: "user", Content: "继续"})
	m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{100, 101}, Changed: th.Messages})
	if len(m.Outbox) != 0 {
		t.Fatal("new message did not confirm delivery after history reload")
	}
}
