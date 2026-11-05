package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
	"miya/internal/desktop"
	"miya/internal/remote"
)

func windowJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 320, 180)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDesktopWindowPreviewRequiresPendingApprovalInConversation(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.setThreads([]Thread{{ID: 1, ServerID: "one"}, {ID: 2, ServerID: "two"}})
	a := codex.Approval{ID: json.RawMessage(`"password"`), ThreadID: "one", Method: "miya/desktopControl", Reason: "密码填写"}
	c.setApprovals([]codex.Approval{a})
	r := remote.Request{ServerID: "one", Approval: a.ID, ApprovalVersion: approvalFingerprint(a)}
	if !c.phoneDesktopApproval(r) {
		t.Fatal("valid approval rejected")
	}
	r.ServerID = "two"
	if c.phoneDesktopApproval(r) {
		t.Fatal("cross-conversation preview accepted")
	}
	r.ServerID = "one"
	r.ApprovalVersion = "stale"
	if c.phoneDesktopApproval(r) {
		t.Fatal("stale approval accepted")
	}
	r.ApprovalVersion = approvalFingerprint(a)
	c.setApprovals(nil)
	if c.phoneDesktopApproval(r) {
		t.Fatal("completed approval preview accepted")
	}
}

func TestDesktopMobileAuthorizationWindowPreview(t *testing.T) {
	img, err := ui.DecodeImage(windowJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.state.DesktopControl = true
		m.state.DesktopWindows = true
		th := m.state.Projects[0].Threads[0]
		m.openThread(th)
		m.showThread(th)
		a := codex.Approval{ID: json.RawMessage(`"password"`), ThreadID: th.ServerID, Method: "miya/desktopControl", Reason: "填写系统密码"}
		m.state.Approvals = []codex.Approval{a}
		m.desktopPreviews = map[string]*mobileWindowPreview{string(a.ID): {version: approvalFingerprint(a), image: img, window: desktop.Window{ID: 21, PID: 80, App: "系统设置", Title: "系统设置 · 授权密码"}, dialog: desktop.Dialog{Title: "要允许此应用作为管理员访问吗？", Message: "输入你的密码以继续。", Actions: []desktop.Action{{ID: "允许访问", Label: "允许访问", Enabled: false, Default: true}, {ID: "不允许", Label: "不允许", Enabled: true, Cancel: true}}}}}
		tt := ui.NewTester(m.viewRoot, width, 844)
		tt.Frame()
		for _, label := range []string{"Control permission window", "Enter permission details", "不允许"} {
			r, ok := tt.Find(label)
			if !ok || r.H < 44 || r.X < 0 || r.X+r.W > float32(width) {
				t.Fatalf("preview action missing at %d: %s %v", width, label, r)
			}
		}
		for _, label := range []string{"Select window", "Done, continue", "刷新预览"} {
			if _, ok := tt.Find(label); ok {
				t.Fatalf("extra control rendered: %s", label)
			}
		}
		saveMobileNativeSnapshot(t, tt, "authorization-window-preview")
		m.approvalAnswered(a.ID, "")
		if len(m.desktopPreviews) != 0 {
			t.Fatal("completed password window preview retained")
		}
	}
}
