package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"miya/internal/codex"
	"miya/internal/remote"
)

// openTestDesktopPage stands in for an open native control page whose Mac is
// unreachable; closing it counts through its context cancel.
func openTestDesktopPage(m *mobileApp, closed *int) {
	offline := func(context.Context) (net.Conn, error) { return nil, errors.New("offline") }
	ctx, cancel := context.WithCancel(context.Background())
	m.desktopPage = &mobileDesktopPage{phase: "live", client: newDesktopClient(offline, "test"), ctx: ctx, cancel: func() { *closed++; cancel() }}
}

func TestMobileDesktopReturnsOnlyWhenExactApprovalResolves(t *testing.T) {
	m := testMobileApp()
	a := codex.Approval{ID: json.RawMessage(`"authorization"`), ThreadID: "s1", Method: "miya/desktopControl", Reason: "Enter permission details"}
	m.state.Approvals = []codex.Approval{a}
	m.desktopApproval = mobileDesktopApproval{ID: a.ID, Version: approvalFingerprint(a)}
	closed := 0
	openTestDesktopPage(m, &closed)
	m.desktopRequest = "opening"
	// Wrong passwords and intermediate native buttons keep this request pending.
	m.applyPhoneState(phoneState{Approvals: []codex.Approval{a}})
	if closed != 0 || m.desktopPage == nil || m.desktopRequest == "" {
		t.Fatal("pending authorization closed its input page")
	}
	m.approvalAnswered(json.RawMessage(`"other"`), "")
	if closed != 0 || m.desktopPage == nil {
		t.Fatal("another approval closed this input page")
	}
	m.approvalAnswered(a.ID, "密码不正确")
	if closed != 0 || m.desktopPage == nil {
		t.Fatal("a rejected submission closed the input page")
	}
	m.applyPhoneState(phoneState{Approvals: []codex.Approval{}})
	if closed != 1 || m.desktopPage != nil || len(m.desktopApproval.ID) != 0 || m.desktopRequest != "" {
		t.Fatal("resolved authorization did not dismiss its control page")
	}
	m.applyPhoneState(phoneState{})
	if closed != 1 {
		t.Fatal("control page dismissed more than once")
	}
}

func TestMobileDesktopReusedApprovalIDClosesStalePage(t *testing.T) {
	m := testMobileApp()
	a := codex.Approval{ID: json.RawMessage(`"authorization"`), ThreadID: "s1", Method: "miya/desktopControl", Reason: "old window"}
	m.desktopApproval = mobileDesktopApproval{ID: a.ID, Version: approvalFingerprint(a)}
	closed := 0
	openTestDesktopPage(m, &closed)
	a.Reason = "different window"
	m.closeResolvedDesktopControl([]codex.Approval{a})
	if closed != 1 || m.desktopPage != nil {
		t.Fatal("stale authorization page was retained for a reused request ID")
	}
}

func TestMobileOtherBrowserRemainsOpenWhenApprovalResolves(t *testing.T) {
	m := testMobileApp()
	closed := false
	m.filesCancel = func() { closed = true }
	pages := 0
	openTestDesktopPage(m, &pages)
	m.applyPhoneState(phoneState{})
	if closed || pages != 0 || m.desktopPage == nil {
		t.Fatal("approval state dismissed an unrelated file browser or full desktop page")
	}
}

func TestDesktopPageCloseRevokesLeaseBeforeReopen(t *testing.T) {
	var sent []string
	old := sendDesktopClose
	sendDesktopClose = func(_ *remote.Conn, token string) { sent = append(sent, token) }
	t.Cleanup(func() { sendDesktopClose = old })
	m := testMobileApp()
	closed := 0
	openTestDesktopPage(m, &closed)
	conn := new(remote.Conn)
	m.conn, m.desktopPage.conn = conn, conn
	p := m.desktopPage
	m.desktopFinish(p, "Remote control ended")
	m.closeDesktopPage(false)
	if len(sent) != 1 || sent[0] != "test" || len(m.desktopClosing) != 1 || m.desktopClosing[0] != "test" {
		t.Fatal("closing did not queue one desktop-close", sent, m.desktopClosing)
	}
	if m.desktopPage != nil {
		t.Fatal("page kept after close")
	}
}
