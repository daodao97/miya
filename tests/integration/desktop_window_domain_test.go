//go:build !mygo_ui

package integration

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	codex "miya/internal/agent/codex/native"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"testing"
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
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "one"}, {ID: 2, ServerID: "two"}})
	a := codex.Approval{ID: json.RawMessage(`"password"`), ThreadID: "one", Method: "miya/desktopControl", Reason: "密码填写"}
	c.SetApprovals([]codex.Approval{a})
	r := remote.Request{ServerID: "one", Approval: a.ID, ApprovalVersion: phoneproto.ApprovalFingerprint(a)}
	if !c.PhoneDesktopApproval(r) {
		t.Fatal("valid approval rejected")
	}
	r.ServerID = "two"
	if c.PhoneDesktopApproval(r) {
		t.Fatal("cross-conversation preview accepted")
	}
	r.ServerID = "one"
	r.ApprovalVersion = "stale"
	if c.PhoneDesktopApproval(r) {
		t.Fatal("stale approval accepted")
	}
	r.ApprovalVersion = phoneproto.ApprovalFingerprint(a)
	c.SetApprovals(nil)
	if c.PhoneDesktopApproval(r) {
		t.Fatal("completed approval preview accepted")
	}
}
