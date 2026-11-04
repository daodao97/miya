package main

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
	"miya/internal/desktop"
)

func TestMobileWindowPreviewFailureKeepsLastFrameAndButtons(t *testing.T) {
	m := testMobileApp()
	data := windowJPEG(t)
	img, err := ui.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	a := codex.Approval{ID: json.RawMessage(`"preview"`), ThreadID: "s1", Method: "miya/desktopControl"}
	p := &mobileWindowPreview{requestID: "refresh", version: approvalFingerprint(a), loading: true, image: img, imageHash: sha256.Sum256(data), window: desktop.Window{ID: 1, PID: 2}, dialog: desktop.Dialog{Title: "授权", Actions: []desktop.Action{{ID: "allow", Label: "允许访问", Enabled: true}}}}
	m.desktopPreviews = map[string]*mobileWindowPreview{string(a.ID): p}
	m.state.Approvals = []codex.Approval{a}
	m.receiveWindowPreview(nil, phoneDesktopPreview{RequestID: "refresh", Approval: a.ID}, nil, "采集暂时不可用")
	if !p.stale || p.loading || p.image != img || p.window.ID != 1 || len(p.dialog.Actions) != 1 || p.message == "" {
		t.Fatal("failed refresh discarded the visible preview", p)
	}
	// Late duplicate replies cannot replace the retained frame or restart it.
	m.receiveWindowPreview(nil, phoneDesktopPreview{RequestID: "refresh", Approval: a.ID}, nil, "another failure")
	if p.message != "采集暂时不可用" {
		t.Fatal("duplicate reply changed paused preview")
	}
	newer := &mobileWindowPreview{requestID: "new"}
	m.desktopPreviews[string(a.ID)] = newer
	m.failWindowPreview(string(a.ID), p, "old timeout")
	if newer.stale || newer.message != "" {
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
	p := &mobileWindowPreview{requestID: "refresh", loading: true, stale: true, image: first, imageHash: sha256.Sum256(data), message: "old failure"}
	m.desktopPreviews = map[string]*mobileWindowPreview{string(id): p}
	m.receiveWindowPreview(nil, phoneDesktopPreview{RequestID: "refresh", Approval: id, Data: data, Window: desktop.Window{ID: 1, PID: 2}, Dialog: desktop.Dialog{Title: "最新状态"}}, second, "")
	if p.image != first || p.loading || p.stale || p.message != "" || p.dialog.Title != "最新状态" {
		t.Fatal("unchanged frame was replaced or successful retry kept stale state")
	}
}
