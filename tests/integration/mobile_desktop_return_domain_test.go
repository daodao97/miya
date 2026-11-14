//go:build !mygo_ui

package integration

import (
	"context"
	"encoding/json"
	"errors"
	codex "miya/internal/agent/codex/native"
	"miya/internal/desktop/control"
	mobile "miya/internal/phone/client"
	phoneproto "miya/internal/phone/protocol"
	"net"
	"testing"
)

// openTestDesktopPage stands in for an open native control page whose Mac is
// unreachable; closing it counts through its context cancel.
func openTestDesktopPage(m *mobile.Client, closed *int) {
	offline := func(context.Context) (net.Conn, error) { return nil, errors.New("offline") }
	ctx, cancel := context.WithCancel(context.Background())
	m.DesktopPage = &mobile.MobileDesktopPage{Phase: "live", Client: control.NewClient(offline, "test"), Ctx: ctx, Cancel: func() { *closed++; cancel() }}
}

func TestMobileDesktopReturnsOnlyWhenExactApprovalResolves(t *testing.T) {
	m := testPhoneClient()
	a := codex.Approval{ID: json.RawMessage(`"authorization"`), ThreadID: "s1", Method: "miya/desktopControl", Reason: "Enter permission details"}
	m.State.Approvals = []codex.Approval{a}
	m.DesktopApproval = mobile.MobileDesktopApproval{ID: a.ID, Version: phoneproto.ApprovalFingerprint(a)}
	closed := 0
	openTestDesktopPage(m, &closed)
	m.DesktopRequest = "opening"
	// Wrong passwords and intermediate native buttons keep this request pending.
	m.ApplyPhoneState(phoneproto.State{Approvals: []codex.Approval{a}})
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
	m.ApplyPhoneState(phoneproto.State{Approvals: []codex.Approval{}})
	if closed != 1 || m.DesktopPage != nil || len(m.DesktopApproval.ID) != 0 || m.DesktopRequest != "" {
		t.Fatal("resolved authorization did not dismiss its control page")
	}
	m.ApplyPhoneState(phoneproto.State{})
	if closed != 1 {
		t.Fatal("control page dismissed more than once")
	}
}

func TestMobileDesktopReusedApprovalIDClosesStalePage(t *testing.T) {
	m := testPhoneClient()
	a := codex.Approval{ID: json.RawMessage(`"authorization"`), ThreadID: "s1", Method: "miya/desktopControl", Reason: "old window"}
	m.DesktopApproval = mobile.MobileDesktopApproval{ID: a.ID, Version: phoneproto.ApprovalFingerprint(a)}
	closed := 0
	openTestDesktopPage(m, &closed)
	a.Reason = "different window"
	m.CloseResolvedDesktopControl([]codex.Approval{a})
	if closed != 1 || m.DesktopPage != nil {
		t.Fatal("stale authorization page was retained for a reused request ID")
	}
}
