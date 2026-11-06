package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"miya/internal/agent/codex/native"
	"miya/internal/desktop/control"
	"miya/internal/mobile"
	"miya/internal/phoneapi"
	"miya/internal/remote"
)

// openTestDesktopPage stands in for an open native control page whose Mac is
// unreachable; closing it counts through its context cancel.
func openTestDesktopPage(m *mobile.App, closed *int) {
	offline := func(context.Context) (net.Conn, error) { return nil, errors.New("offline") }
	ctx, cancel := context.WithCancel(context.Background())
	m.DesktopPage = &mobile.MobileDesktopPage{Phase: "live", Client: control.NewClient(offline, "test"), Ctx: ctx, Cancel: func() { *closed++; cancel() }}
}

func TestMobileDesktopReturnsOnlyWhenExactApprovalResolves(t *testing.T) {
	m := testMobileApp()
	a := codex.Approval{ID: json.RawMessage(`"authorization"`), ThreadID: "s1", Method: "miya/desktopControl", Reason: "Enter permission details"}
	m.State.Approvals = []codex.Approval{a}
	m.DesktopApproval = mobile.MobileDesktopApproval{ID: a.ID, Version: phoneapi.ApprovalFingerprint(a)}
	closed := 0
	openTestDesktopPage(m, &closed)
	m.DesktopRequest = "opening"
	// Wrong passwords and intermediate native buttons keep this request pending.
	m.ApplyPhoneState(phoneapi.State{Approvals: []codex.Approval{a}})
	if closed != 0 || m.DesktopPage == nil || m.DesktopRequest == "" {
		t.Fatal("pending authorization closed its input page")
	}
	m.ApprovalAnswered(json.RawMessage(`"other"`), "")
	if closed != 0 || m.DesktopPage == nil {
		t.Fatal("another approval closed this input page")
	}
	m.ApprovalAnswered(a.ID, "密码不正确")
	if closed != 0 || m.DesktopPage == nil {
		t.Fatal("a rejected submission closed the input page")
	}
	m.ApplyPhoneState(phoneapi.State{Approvals: []codex.Approval{}})
	if closed != 1 || m.DesktopPage != nil || len(m.DesktopApproval.ID) != 0 || m.DesktopRequest != "" {
		t.Fatal("resolved authorization did not dismiss its control page")
	}
	m.ApplyPhoneState(phoneapi.State{})
	if closed != 1 {
		t.Fatal("control page dismissed more than once")
	}
}

func TestMobileDesktopReusedApprovalIDClosesStalePage(t *testing.T) {
	m := testMobileApp()
	a := codex.Approval{ID: json.RawMessage(`"authorization"`), ThreadID: "s1", Method: "miya/desktopControl", Reason: "old window"}
	m.DesktopApproval = mobile.MobileDesktopApproval{ID: a.ID, Version: phoneapi.ApprovalFingerprint(a)}
	closed := 0
	openTestDesktopPage(m, &closed)
	a.Reason = "different window"
	m.CloseResolvedDesktopControl([]codex.Approval{a})
	if closed != 1 || m.DesktopPage != nil {
		t.Fatal("stale authorization page was retained for a reused request ID")
	}
}

func TestMobileOtherBrowserRemainsOpenWhenApprovalResolves(t *testing.T) {
	m := testMobileApp()
	closed := false
	m.FilesCancel = func() { closed = true }
	pages := 0
	openTestDesktopPage(m, &pages)
	m.ApplyPhoneState(phoneapi.State{})
	if closed || pages != 0 || m.DesktopPage == nil {
		t.Fatal("approval state dismissed an unrelated file browser or full desktop page")
	}
}

func TestDesktopPageCloseRevokesLeaseBeforeReopen(t *testing.T) {
	var sent []string
	old := mobile.SendDesktopClose
	mobile.SendDesktopClose = func(_ *remote.Conn, token string) { sent = append(sent, token) }
	t.Cleanup(func() { mobile.SendDesktopClose = old })
	m := testMobileApp()
	closed := 0
	openTestDesktopPage(m, &closed)
	conn := new(remote.Conn)
	m.Conn, m.DesktopPage.Conn = conn, conn
	p := m.DesktopPage
	m.DesktopFinish(p, "Remote control ended")
	m.CloseDesktopPage(false)
	if len(sent) != 1 || sent[0] != "test" || len(m.DesktopClosing) != 1 || m.DesktopClosing[0] != "test" {
		t.Fatal("closing did not queue one desktop-close", sent, m.DesktopClosing)
	}
	if m.DesktopPage != nil {
		t.Fatal("page kept after close")
	}
}
