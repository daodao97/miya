package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/codex"
	"miya/internal/desktop"
	"miya/internal/mobile"
	"miya/internal/phoneapi"
)

func TestMobileToolPermissionKeepsActionsVisibleWithoutUnrelatedPreview(t *testing.T) {
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.State.DesktopControl = true
		m.State.DesktopWindows = true
		th := m.State.Projects[0].Threads[0]
		m.OpenThread(th)
		m.ShowThread(th)
		a := codex.Approval{ID: json.RawMessage(`"tool-consent"`), ThreadID: th.ServerID, Method: "mcpServer/elicitation/request", Mode: "form", Message: "Allow Computer Use to use Miya Window QA? " + strings.Repeat("这是一段需要滚动查看的工具权限说明。", 100), RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`)}
		m.State.Approvals = []codex.Approval{a}
		m.DesktopPreviews = map[string]*mobile.MobileWindowPreview{string(a.ID): {Version: phoneapi.ApprovalFingerprint(a), Dialog: desktop.Dialog{Title: "无关的 Apple 账户", Message: "Wi-Fi\n蓝牙\n网络\n侧栏内容"}}}
		tt := ui.NewTester(m.ViewRoot, width, 700)
		tt.SetDark(true)
		tt.Frame()
		for _, label := range []string{"无关的 Apple 账户", "侧栏内容", "Control permission window"} {
			if tt.HasText(label) {
				t.Fatalf("tool consent showed unrelated window: %s", label)
			}
		}
		if tt.HasText("Review and confirm") || m.Router().Path() != "/thread" {
			t.Fatal("simple MCP consent should show its actions directly in the conversation")
		}
		for _, label := range []string{"Confirm", "Deny", "Cancel request"} {
			r, ok := tt.Find(label)
			if !ok || r.H < 44 || r.Y < 64 || r.Y+r.H > 700 || r.X < 0 || r.X+r.W > float32(width) {
				t.Fatalf("permission action off-screen at %d: %s %v", width, label, r)
			}
		}
		for _, label := range []string{"无关的 Apple 账户", "侧栏内容", "Control permission window"} {
			if tt.HasText(label) {
				t.Fatalf("permission page showed unrelated window: %s", label)
			}
		}
		saveMobileNativeSnapshot(t, tt, "tool-permission-phone")
	}
}

func TestMobileMCPAccessActionsStayInConversation(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{320, 390} {
			t.Run(fmt.Sprintf("dark=%v/width=%d", dark, width), func(t *testing.T) {
				m := testMobileApp()
				a := appAccessRequest()
				a.ThreadID = "s1"
				m.State.Approvals = []codex.Approval{a}
				m.OpenThread(m.State.Projects[0].Threads[0])
				tt := ui.NewTester(m.ViewRoot, width, 750)
				tt.SetDark(dark)
				tt.Frame()
				for _, label := range []string{"Allow once", "Deny", "Always allow in this conversation"} {
					r, ok := tt.Find(label)
					if !ok || r.H < 44 || r.X < 0 || r.X+r.W > float32(width) || r.Y+r.H > 750 {
						t.Fatalf("inline permission action missing or clipped: %s %v", label, r)
					}
				}
				if tt.HasText("Review and confirm") || m.Router().Path() != "/thread" || len(m.State.Approvals) != 1 {
					t.Fatal("MCP consent navigated away or was accepted before a click")
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("mcp-inline-%v-%d", dark, width))
				if err := tt.Click("Allow once"); err != nil {
					t.Fatal(err)
				}
				if m.Router().Path() != "/thread" || len(m.State.Approvals) != 1 || m.FormErrors[string(a.ID)] != "Not connected to desktop" {
					t.Fatal("failed inline submission navigated away or consumed the approval")
				}
			})
		}
	}
}

func TestMobileMCPFormsAndURLsKeepTheirInputPage(t *testing.T) {
	for _, mode := range []string{"form", "url"} {
		m := testMobileApp()
		a := appAccessRequest()
		a.ThreadID, a.Mode = "s1", mode
		if mode == "form" {
			a.RequestedSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","title":"姓名"}},"required":["name"]}`)
		} else {
			a.URL = "https://example.com/authorize"
		}
		m.State.Approvals = []codex.Approval{a}
		m.OpenThread(m.State.Projects[0].Threads[0])
		tt := ui.NewTester(m.ViewRoot, 390, 750)
		tt.Frame()
		if tt.HasText("Allow once") || tt.HasText("Always allow in this conversation") {
			t.Fatal("a form or browser authorization was flattened into simple consent")
		}
		if err := tt.Click("Review and confirm"); err != nil {
			t.Fatal(err)
		}
		if m.Router().Path() != "/approval" {
			t.Fatal("form and URL requests lost their input page")
		}
	}
}

func TestMobileAuthorizationAllowClearlyDiffersFromDisabled(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := testMobileApp()
		m.ApplyPalette(dark)
		pressed, enabled := 0, false
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Padding(20).Background(m.Pal.Bg).Children(func() {
				if m.ApprovalButton(c, "allow", "允许访问", true, !enabled).Clicked() {
					pressed++
				}
			})
		}, 320, 160)
		tt.Frame()
		r, _ := tt.Find("允许访问")
		disabledColor := tt.Image().RGBAAt(int(r.X+10), int(r.Y+r.H/2))
		if err := tt.Click("允许访问"); err != nil {
			t.Fatal(err)
		}
		if pressed != 0 {
			t.Fatal("disabled authorization action accepted a click")
		}
		enabled = true
		tt.Frame()
		r, _ = tt.Find("允许访问")
		activeColor := tt.Image().RGBAAt(int(r.X+10), int(r.Y+r.H/2))
		if int(activeColor.B)-int(activeColor.R) < 30 || activeColor == disabledColor {
			t.Fatalf("available approval still looks disabled: active %v disabled %v", activeColor, disabledColor)
		}
		if err := tt.Click("允许访问"); err != nil {
			t.Fatal(err)
		}
		if pressed != 1 {
			t.Fatal("available authorization action did not accept a click")
		}
	}
}

func TestMobileAuthorizationOffersInputBeforeAllow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{320, 390} {
			t.Run(fmt.Sprintf("dark=%v/width=%d", dark, width), func(t *testing.T) {
				m := testMobileApp()
				m.State.DesktopControl, m.State.DesktopWindows = true, true
				thread := m.State.Projects[0].Threads[0]
				m.OpenThread(thread)
				m.ShowThread(thread)
				a := codex.Approval{ID: json.RawMessage(`"input-first"`), ThreadID: thread.ServerID, Method: "miya/desktopControl"}
				p := &mobile.MobileWindowPreview{Version: phoneapi.ApprovalFingerprint(a), Window: desktop.Window{ID: 21, PID: 80},
					Dialog: desktop.Dialog{Title: "测试授权", Actions: []desktop.Action{
						{ID: "allow", Default: true, Label: "允许访问", Enabled: false},
						{ID: "deny", Cancel: true, Label: "不允许", Enabled: true},
					}}}
				m.State.Approvals = []codex.Approval{a}
				m.DesktopPreviews = map[string]*mobile.MobileWindowPreview{string(a.ID): p}
				tt := ui.NewTester(m.ViewRoot, width, 750)
				tt.SetDark(dark)
				tt.Frame()
				r, ok := tt.Find("Enter permission details")
				if !ok || r.H < 44 || r.X < 0 || r.X+r.W > float32(width) || r.Y+r.H > 750 {
					t.Fatalf("no visible input action before native allow is enabled: %v", r)
				}
				color := tt.Image().RGBAAt(int(r.X+10), int(r.Y+r.H/2))
				if int(color.B)-int(color.R) < 30 {
					t.Fatalf("input entry looks disabled: %v", color)
				}
				if _, ok := tt.Find("允许访问"); ok {
					t.Fatal("disabled native allow still presented as the next action")
				}
				if !tt.HasText("不允许") {
					t.Fatal("the input entry removed the native deny action")
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("auth-input-entry-%v-%d", dark, width))
				if err := tt.Click("Enter permission details"); err != nil {
					t.Fatal(err)
				}
				if p.ActionPending || m.ApprovalPending[string(a.ID)] || len(m.State.Approvals) != 1 {
					t.Fatal("opening the input entry submitted the disabled native approval")
				}
				// A fresh native state, rather than local typing, enables consent.
				p.Dialog.Actions[0].Enabled = true
				tt.Frame()
				if tt.HasText("Enter permission details") {
					t.Fatal("input entry remained after the native allow became enabled")
				}
				r, ok = tt.Find("允许访问")
				if !ok {
					t.Fatal("enabled native allow is missing")
				}
				color = tt.Image().RGBAAt(int(r.X+10), int(r.Y+r.H/2))
				if int(color.B)-int(color.R) < 30 {
					t.Fatal("enabled native allow is not blue")
				}
			})
		}
	}
}

func TestMobileAuthorizationLongCopyDoesNotBuryNativeButtons(t *testing.T) {
	img, err := ui.DecodeImage(windowJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.State.DesktopControl = true
		m.State.DesktopWindows = true
		th := m.State.Projects[0].Threads[0]
		m.OpenThread(th)
		m.ShowThread(th)
		a := codex.Approval{ID: json.RawMessage(`"auth"`), ThreadID: th.ServerID, Method: "miya/desktopControl"}
		m.State.Approvals = []codex.Approval{a}
		m.DesktopPreviews = map[string]*mobile.MobileWindowPreview{string(a.ID): {Version: phoneapi.ApprovalFingerprint(a), Image: img, Dialog: desktop.Dialog{Title: strings.Repeat("这是很长的授权窗口标题", 10), Message: strings.Repeat("权限的完整说明。\n", 40), Actions: []desktop.Action{{ID: "allow", Default: true, Label: "允许访问", Enabled: true}, {ID: "deny", Cancel: true, Label: "不允许", Enabled: true}}}}}
		tt := ui.NewTester(m.ViewRoot, width, 750)
		tt.SetDark(true)
		tt.Frame()
		for _, label := range []string{"Control permission window", "允许访问", "不允许", "Full description"} {
			r, ok := tt.Find(label)
			if !ok || r.H < 44 || r.Y < 0 || r.Y+r.H > 750 {
				t.Fatalf("long copy hid %s at %d: %v", label, width, r)
			}
		}
		saveMobileNativeSnapshot(t, tt, "auth-window-phone")
	}
}
