package integration

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"

	codex "miya/internal/agent/codex/native"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/mobile"
	phoneproto "miya/internal/phone/protocol"
)

func TestMobileWindowPreviewFailureKeepsLastFrameAndButtons(t *testing.T) {
	m := testMobileApp()
	data := windowJPEG(t)
	img, err := ui.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	a := codex.Approval{ID: json.RawMessage(`"preview"`), ThreadID: "s1", Method: "miya/desktopControl"}
	p := &mobile.MobileWindowPreview{RequestID: "refresh", Version: phoneproto.ApprovalFingerprint(a), Loading: true, Image: img, ImageHash: sha256.Sum256(data), Window: desktop.Window{ID: 1, PID: 2}, Dialog: desktop.Dialog{Title: "授权", Actions: []desktop.Action{{ID: "allow", Label: "允许访问", Enabled: true}}}}
	m.DesktopPreviews = map[string]*mobile.MobileWindowPreview{string(a.ID): p}
	m.State.Approvals = []codex.Approval{a}
	m.ReceiveWindowPreview(nil, control.Preview{RequestID: "refresh", Approval: a.ID}, nil, "采集暂时不可用")
	if !p.Stale || p.Loading || p.Image != img || p.Window.ID != 1 || len(p.Dialog.Actions) != 1 || p.Message == "" {
		t.Fatal("failed refresh discarded the visible preview", p)
	}
	// Late duplicate replies cannot replace the retained frame or restart it.
	m.ReceiveWindowPreview(nil, control.Preview{RequestID: "refresh", Approval: a.ID}, nil, "another failure")
	if p.Message != "采集暂时不可用" {
		t.Fatal("duplicate reply changed paused preview")
	}
	newer := &mobile.MobileWindowPreview{RequestID: "new"}
	m.DesktopPreviews[string(a.ID)] = newer
	m.FailWindowPreview(string(a.ID), p, "old timeout")
	if newer.Stale || newer.Message != "" {
		t.Fatal("old timeout reached new preview")
	}
}

func TestMobileWindowPreviewUnchangedFrameReusesVisibleImage(t *testing.T) {
	m := testMobileApp()
	data := windowJPEG(t)
	first, err := ui.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ui.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	id := json.RawMessage(`"preview"`)
	p := &mobile.MobileWindowPreview{RequestID: "refresh", Loading: true, Stale: true, Image: first, ImageHash: sha256.Sum256(data), Message: "old failure"}
	m.DesktopPreviews = map[string]*mobile.MobileWindowPreview{string(id): p}
	m.ReceiveWindowPreview(nil, control.Preview{RequestID: "refresh", Approval: id, Data: data, Window: desktop.Window{ID: 1, PID: 2}, Dialog: desktop.Dialog{Title: "最新状态"}}, second, "")
	if p.Image != first || p.Loading || p.Stale || p.Message != "" || p.Dialog.Title != "最新状态" {
		t.Fatal("unchanged frame was replaced or successful retry kept stale state")
	}
}
