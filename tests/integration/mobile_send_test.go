package integration

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"miya/internal/uiruntime"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/mobile"
	phoneproto "miya/internal/phone/protocol"
	phoneserver "miya/internal/phone/server"
	"miya/internal/widgets"
)

func TestMobileNewSendStaysVisibleUntilHistoryArrives(t *testing.T) {
	for _, agent := range []string{appmodel.AgentCodex, appmodel.AgentClaude} {
		for _, width := range []int{320, 390} {
			t.Run(fmt.Sprintf("%s/%d", agent, width), func(t *testing.T) {
				m := testMobileApp()
				m.NewProject, m.NewAgent = "/a", agent
				m.Router().Push("/thread")
				m.Sending, m.Draft = true, "请修复手机新建对话的空白问题"
				beginMobileSend(m, m.Draft, nil)
				tt := ui.NewTester(m.ViewRoot, width, 750)
				tt.Frame()
				if !tt.HasText("请修复手机新建对话的空白问题") || !tt.HasText("Sending…") || tt.HasText("Let’s build") {
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
				m.ReceiveThread(3, phoneproto.ThreadDelta{Thread: model.Thread{ID: 3, Agent: agent, Loading: true}, Reset: true})
				tt.Frame()
				if !tt.HasText("请修复手机新建对话的空白问题") {
					t.Fatal("a loading delta hid the local message")
				}
				m.ReceiveThread(3, phoneproto.ThreadDelta{Thread: model.Thread{ID: 3, ServerID: "created", Agent: agent, HistoryLoaded: true, Busy: true}, Reset: true,
					Order: []int{10}, Changed: []model.Message{{ID: 10, Role: "user", Content: "请修复手机新建对话的空白问题"}}})
				tt.Frame()
				if len(m.Outbox) != 0 || !tt.HasText("请修复手机新建对话的空白问题") || tt.HasText("Syncing conversation…") {
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
	beginMobileSend(m, m.Draft, nil)
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	m.Sent(3, "")
	// Codex publishes the local user message before thread/start returns
	// its durable ID. The phone has already subscribed by the numeric ID.
	sync := phoneserver.NewThreadSync(nil)
	thread := model.Thread{ID: 3, Agent: appmodel.AgentCodex, HistoryLoaded: true, Busy: true,
		Messages: []model.Message{{ID: 10, Role: "user", Content: "首条消息应一直可见", ItemID: "pending-user"}}}
	delta, changed := sync.Update(thread)
	if !changed {
		t.Fatal("initial user message was not published")
	}
	m.ReceiveThread(3, delta)
	tt.Frame()
	if len(m.Outbox) != 0 || !tt.HasText("首条消息应一直可见") {
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
	thread.Messages = append(thread.Messages, model.Message{ID: 11, Role: "assistant", Content: "正在处理"})
	delta, _ = sync.Update(thread)
	m.ReceiveThread(3, delta)
	tt.Frame()
	messages := m.Viewer.Current().Messages
	if !tt.HasText("首条消息应一直可见") || len(messages) != 2 || messages[1].Content != "正在处理" {
		t.Fatal("subsequent streaming lost or duplicated the first message")
	}
}

func TestMobileSendFailureKeepsMessagePhotosAndNextDraft(t *testing.T) {
	m := testMobileApp()
	m.NewProject, m.NewAgent = "/a", appmodel.AgentCodex
	m.Router().Push("/thread")
	photo := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(photo, []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Draft, m.Images = "保留这条消息", []string{photo}
	beginMobileSend(m, m.Draft, m.Images)
	m.Draft = "正在写下一条"
	failure := "Desktop is not ready. Try again shortly."
	m.Sent(0, failure)
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	if len(m.Outbox) != 1 || m.Sending || m.Draft != "正在写下一条" || len(m.Images) != 0 || !tt.HasText(failure) || !tt.HasText("保留这条消息") || !tt.HasText("Retry send") {
		t.Fatalf("failed send lost content or next draft: %v", tt.Texts())
	}
	if _, err := os.Stat(photo); err != nil {
		t.Fatal("failed send deleted the photo needed for retry")
	}
	saveMobileNativeSnapshot(t, tt, "send-failed-phone")
	m.Home()
	if _, ok := m.PendingSendThread(m.Viewer.Current()); ok {
		t.Fatal("leaving the conversation displayed an unrelated pending message")
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

func beginMobileSend(m *mobile.App, text string, images []string) {
	r := m.ThreadRequest("send", m.Open)
	r.Text = text
	if m.Open == 0 {
		r.Project, r.Agent = m.NewProject, m.NewAgent
	}
	m.BeginSend(r, images)
}

func TestMobileExistingSendAppearsBeforeUploadAndSurvivesAcknowledgement(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			th := m.State.Projects[0].Threads[0]
			th.HistoryLoaded = true
			th.Messages = []model.Message{{ID: 1, Role: "user", Content: "旧消息"}, {ID: 2, Role: "assistant", Content: "上一次的回答"}}
			m.OpenThread(th)
			m.ShowThread(th)
			m.Draft = "慢网下也要立即显示的新消息"
			beginMobileSend(m, m.Draft, nil)
			tt := ui.NewTester(m.ViewRoot, width, 750)
			if m.Draft != "" || !tt.HasText("Sending…") || !tt.HasText("慢网下也要立即显示的新消息") || len(m.Viewer.Current().Messages) != 2 {
				t.Fatalf("pending send not projected at once: %v", tt.Texts())
			}
			shown, _ := m.PendingSendThread(m.Viewer.Current())
			if len(shown.Messages) != 3 || shown.Messages[0].Content != "旧消息" {
				t.Fatal("pending preview replaced the existing history")
			}
			if len(m.SubscribeRequest(th.ID).Have) > 2 {
				t.Fatal("pending message entered synchronized history")
			}
			m.Draft = "尚未发送的下一条"
			m.Sent(th.ID, "")
			tt.Frame()
			if m.Draft != "尚未发送的下一条" || !tt.HasText("慢网下也要立即显示的新消息") || !tt.HasText("Syncing conversation…") {
				t.Fatal("acknowledgement cleared the next draft or pending message")
			}
			// An unrelated old user message and metadata must not dismiss the preview.
			m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{1, 2}, Changed: th.Messages})
			if len(m.Outbox) != 1 {
				t.Fatal("old history was mistaken for the new echo")
			}
			th.Messages = append(th.Messages, model.Message{ID: 3, Role: "user", Content: "慢网下也要立即显示的新消息"})
			m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{1, 2, 3}, Changed: th.Messages})
			tt.Frame()
			if len(m.Outbox) != 0 || len(m.Viewer.Current().Messages) != 3 || m.Draft != "尚未发送的下一条" {
				t.Fatal("confirmed message duplicated or next draft lost")
			}
			saveMobileNativeSnapshot(t, tt, "send-confirmed-phone")
		})
	}
}

func TestMobileIdenticalSendsRequireSeparateDesktopEchoes(t *testing.T) {
	m := testMobileApp()
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
			m := testMobileApp()
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
	m := testMobileApp()
	m.NewProject, m.NewAgent = "/a", appmodel.AgentCodex
	m.Router().Push("/thread")
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

func TestMobileImageSendUsesLocalPreviewThroughDesktopConfirmation(t *testing.T) {
	calls := make(chan func(), 1)
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { calls <- f }
	defer func() { uiruntime.RunOnMain = old }()
	m := testMobileApp()
	m.CacheDir = t.TempDir()
	th := m.State.Projects[0].Threads[0]
	th.HistoryLoaded = true
	m.OpenThread(th)
	m.ShowThread(th)
	photo := filepath.Join(t.TempDir(), "photo.png")
	f, err := os.Create(photo)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 160, 100))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 80, G: 145, B: 170, A: 255}), image.Point{}, draw.Src)
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := widgets.LoadImage(photo); err != nil {
		t.Fatal(err)
	}
	m.Images = []string{photo}
	m.Draft = "图片应一直显示"
	beginMobileSend(m, m.Draft, m.Images)
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	if m.MessageImage(photo) == nil || !tt.HasText("Uploading images…") || !tt.HasText("图片应一直显示") {
		t.Fatal("upload did not show the local photo immediately")
	}
	saveMobileNativeSnapshot(t, tt, "send-uploading-phone")
	// A new draft's attachment is independent of the in-flight photo.
	m.Draft, m.Images = "下一张照片的说明", []string{"next-photo.png"}
	m.Sent(th.ID, "")
	if m.Draft != "下一张照片的说明" || len(m.Images) != 1 || m.Images[0] != "next-photo.png" {
		t.Fatal("acknowledgement cleared the next attachment")
	}
	source := "file:///desktop/received-photo.png"
	th.Messages = []model.Message{{ID: 10, Role: "user", Content: "图片应一直显示", Images: []string{source}}}
	m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{10}, Changed: th.Messages})
	if len(m.Outbox) != 0 || m.MessageImage(source) == nil {
		t.Fatal("desktop confirmation caused the already-visible photo to disappear")
	}
	select {
	case f := <-calls:
		f()
	case <-time.After(5 * time.Second):
		t.Fatal("local preview was not cached")
	}
	if m.MessageImage(source) == nil {
		t.Fatal("cached preview replaced the image with a blank")
	}
}

func TestMobilePendingSendReusesHistoryBetweenFrames(t *testing.T) {
	m := testMobileApp()
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
	m := testMobileApp()
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
