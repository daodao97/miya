package nsmobile

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/agent"
	"miya/internal/chat/model"
	"miya/internal/desktop"
	"miya/internal/interaction"
	"miya/internal/notification"
	phoneproto "miya/internal/phone/protocol"
)

func TestMigratedFailedPhotoSendKeepsMessageAndNextDraft(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() {
		m.Draft = "立即出现在对话里"
		m.Images = []string{filepath.Join(t.TempDir(), "missing.png")}
	})
	app.Press("id:send")
	app.WaitUntil(time.Second, func() bool { return !m.Sending })
	if !strings.Contains(phoneTexts(app), "立即出现在对话里") || len(m.Outbox) != 1 || !strings.Contains(phoneTexts(app), "Retry send") {
		t.Fatal(phoneTexts(app))
	}
	app.Type("id:mobile-composer", "next draft")
	changePhone(app, m, func() { m.Sent(7, "") })
	if m.Draft != "next draft" || len(m.Outbox) != 1 {
		t.Fatal("late acknowledgement discarded failed preview or next draft")
	}
}

func TestMigratedRequestFieldsAreVersionAndDesktopScoped(t *testing.T) {
	app, m, sent, a := showPhoneRequest(t, `{"ID":"3","kind":"form","requestedSchema":{"type":"object","properties":{"secret":{"type":"string","title":"Secret"}}}}`)
	app.Type("label:Secret", "private draft")
	if m.RequestFields(a)["secret"] != "private draft" {
		t.Fatal("draft missing")
	}
	next := a
	next.Reason = "different request"
	changePhone(app, m, func() { m.ApplyPhoneState(phoneproto.State{Approvals: []interaction.Request{next}}) })
	if len(m.RequestFields(next)) != 0 || strings.Contains(phoneTexts(app), "private draft") {
		t.Fatal("new request inherited secret")
	}
	changePhone(app, m, func() { m.Link = "another-desktop"; m.change() })
	if len(m.RequestFields(a)) != 0 {
		t.Fatal("another desktop inherited secret")
	}
	select {
	case r := <-sent:
		if r.Op == "respond" {
			t.Fatal("state change granted consent")
		}
	default:
	}
}

func TestMigratedSheetsReturnToConversationWithoutLosingDraft(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	app.Type("id:mobile-composer", "keep this draft")
	before := app.Find("id:mobile-composer").ID
	for _, sheet := range []string{"actions", "options", "settings", "language", "desktops", "memory", "automations"} {
		changePhone(app, m, func() { m.pages.Open(sheet) })
		app.Back()
		app.Settle()
		if m.pages.Top() != "" || m.Open != 7 || m.Draft != "keep this draft" || app.Find("id:mobile-composer").ID != before {
			t.Fatalf("sheet %s changed conversation", sheet)
		}
	}
}

func TestMigratedHomeSearchAcrossCollapsedProjectsAndPagination(t *testing.T) {
	app, m, _ := startPhone(t)
	changePhone(app, m, func() {
		for i := 8; i < 28; i++ {
			m.State.Projects[0].Threads = append(m.State.Projects[0].Threads, model.Thread{ID: i, ServerID: fmt.Sprint(i), Title: fmt.Sprintf("Task %d", i), LastActivityAt: int64(i)})
		}
		m.State.Projects = append(m.State.Projects, model.ProjectSnapshot{Name: "Other", Path: "/other", Threads: []model.Thread{{ID: 40, ServerID: "other", Title: "Hidden project task"}}})
	})
	openPhoneDrawer(app)
	if app.Exists("id:thread-7") {
		t.Fatal("collapsed list did not limit old conversations")
	}
	app.Press("id:more-/work")
	if !app.Exists("id:thread-7") {
		t.Fatal("expand did not show older conversation")
	}
	app.Press("id:drawer-search")
	app.Type("id:home-search", "hidden project")
	if !app.Exists("id:thread-40") || app.Exists("id:thread-7") {
		t.Fatal("search did not include collapsed project")
	}
	app.Press("id:thread-40")
	if m.Open != 40 || m.ComposerProject() != "/other" {
		t.Fatal("cross-project selection lost owner")
	}
}

func TestMigratedHomeActivityAndLargeTextTargets(t *testing.T) {
	app, m, _ := startPhone(t)
	changePhone(app, m, func() {
		m.State.Projects = append(m.State.Projects, model.ProjectSnapshot{Name: strings.Repeat("长项目", 10), Path: "/background", Threads: []model.Thread{{ID: 9, ServerID: "run", Title: strings.Repeat("Long title 中文 ", 10), Busy: true}}})
	})
	app.Resize(320, 750)
	app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, SafeArea: core.Edges{Top: 47, Bottom: 34}, FontScale: 1.8})
	openPhoneDrawer(app)
	v := app.Find("id:activity-9")
	if v.Frame.Size.Height < 44 || v.Frame.Size.Width > 320 {
		t.Fatalf("activity target %+v", v.Frame)
	}
	app.Press("id:activity-9")
	if m.Open != 9 || m.ComposerProject() != "/background" {
		t.Fatal("activity did not open owning project")
	}
}

func TestMigratedCommandSelectionStagesArgumentsWithoutSending(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	// ComposerCommands keys are derived by the client, never guessed by a view.
	changePhone(app, m, func() {
		th := m.Viewer.Current()
		th.Agent = model.AgentClaude
		m.ShowThread(th)
		m.Commands = map[string][]model.ComposerChoice{agent.CommandKey(model.AgentClaude, "/work"): {{Name: "review"}}}
	})

	app.Type("id:mobile-composer", "/rev")
	app.Press("id:command-review")
	if m.Draft != "/review " || m.Sending {
		t.Fatal("completion executed command")
	}
	app.Type("id:mobile-composer", "检查中文")
	if m.Draft != "/review 检查中文" {
		t.Fatal(m.Draft)
	}
	app.Press("id:send")
	r := awaitRequest(t, app, sent, "send")
	if r.Text != "/review 检查中文" {
		t.Fatal(r)
	}
}

func TestMigratedRequestLayoutAndTakeoverActionsAtLargeText(t *testing.T) {
	app, m, sent, a := showPhoneRequest(t, `{"ID":"takeover","Method":"miya/desktopControl","reason":"Complete authorization"}`)
	app.Resize(320, 568)
	app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, FontScale: 1.8, KeyboardHeight: 180, SafeArea: core.Edges{Top: 20, Bottom: 20}})
	for _, id := range []string{"allow", "decline"} {
		v := app.FindMounted("id:" + id)
		if v.Frame.Size.Height < 44 || v.Frame.Size.Width > 320 {
			t.Fatalf("%s target %+v", id, v.Frame)
		}
	}
	if app.Exists("label:Allow once") || !strings.Contains(phoneTexts(app), "Done, continue") {
		t.Fatal("takeover used generic approval actions")
	}
	app.Press("id:decline")
	r := awaitRequest(t, app, sent, "respond")
	if r.Response.Action != interaction.Cancel {
		t.Fatal(r.Response)
	}
	changePhone(app, m, func() { m.ApprovalAnswered(a.ID, "") })
}

func TestMigratedDesktopPickerFiltersAppsAndNamesDisplays(t *testing.T) {
	app, m, p, _ := startDesktop(t, "picker")
	changePhone(app, m, func() {
		p.Pickable = true
		p.AppsAvailable = true
		p.DisplaysAvailable = true
		p.PickerTab = 1
		p.Apps = []desktop.Application{{ID: "a", Name: "Safari", BundleID: "com.apple.Safari"}, {ID: "b", Name: "Terminal", BundleID: "com.apple.Terminal"}}
	})
	app.Type("id:desktop-app-search", "terminal")
	if !app.Exists("id:desktop-app-b") || app.Exists("id:desktop-app-a") {
		t.Fatal("application search failed")
	}
	changePhone(app, m, func() {
		p.PickerTab = 2
		p.Displays = []desktop.Display{{ID: 1, UUID: "primary", Name: "Main display"}, {ID: 2, UUID: "secondary", Name: "External display"}}
	})
	for _, id := range []string{"primary", "secondary"} {
		if !app.Exists("id:desktop-display-" + id) {
			t.Fatal("missing display", id)
		}
	}
}

func TestMigratedUnreadRevisionAndNotificationIdentity(t *testing.T) {
	app, m, _ := startPhone(t)
	changePhone(app, m, func() {
		m.push.disabled = false
		m.ApplyNotifications(notification.PhoneState{Revision: 5, Unread: []notification.Unread{{Key: "a", Version: 5, ServerID: "session"}}})
		m.ApplyNotifications(notification.PhoneState{Revision: 4, Unread: []notification.Unread{}})
	})
	if app.Badge != 1 {
		t.Fatal("old acknowledgement erased unread")
	}
	changePhone(app, m, func() { m.ApplyNotifications(notification.PhoneState{Revision: 6, Unread: []notification.Unread{}}) })
	if app.Badge != 0 {
		t.Fatal("new acknowledgement retained unread")
	}
	data := map[string]string{"thread": "7", "serverId": "session", "desktop": "host#&"}
	key, got, ok := notification.ParseID(notification.ID("n-1", data))
	if !ok || key != "n-1" || got["desktop"] != data["desktop"] {
		t.Fatal("notification routing identity was not lossless")
	}
	if _, _, ok := notification.ParseID("8F2C-APNS-UUID"); ok {
		t.Fatal("foreign notification treated as local routing data")
	}
}

func TestMigratedManualDisconnectEndsDesktopLease(t *testing.T) {
	app, m, p, _ := startDesktop(t, "live")
	changePhone(app, m, func() { p.Floating = true; m.Path = "/thread"; m.pages.Open("settings") })
	app.Press("id:disconnect")
	if m.DesktopPage != nil || p.Ctx.Err() == nil {
		t.Fatal("manual disconnect retained resumable desktop lease")
	}
}

func TestMigratedQuotaInspectionDoesNotChangeAgent(t *testing.T) {
	app, m, _ := startPhone(t)
	openPhoneDrawer(app)
	if app.Exists("id:drawer-quotas") || app.Exists("id:quota") {
		t.Fatal("usage should take no space until there are quota windows")
	}
	reset := time.Date(2026, 11, 25, 14, 30, 0, 0, time.Local).Unix()
	changePhone(app, m, func() {
		m.Connected, m.Error = true, ""
		m.State.Agent = model.AgentClaude
		m.State.Quotas = []phoneproto.AgentQuota{
			{Agent: model.AgentCodex, Windows: []phoneproto.QuotaWindow{{Label: "5-hour quota", UsedPercent: 34}, {Label: "Weekly", UsedPercent: 81, ResetsAt: reset}}},
			{Agent: model.AgentClaude, Windows: []phoneproto.QuotaWindow{{Label: "Weekly", UsedPercent: 100}}},
			{Agent: "empty"},
		}
	})
	if app.Exists("id:drawer-quota-empty") || app.Find("id:drawer-quota-remaining-codex").Text != "66" ||
		app.Find("id:drawer-quota-remaining-"+model.AgentClaude).Text != "0" {
		t.Fatal("drawer quotas should show remaining use for agents with quota windows")
	}
	captureNativePage(app, "drawer-quotas")
	app.Press("id:drawer-quota-codex")
	panel := app.WaitFor("id:drawer-quota-details", time.Second)
	trigger := app.Find("id:drawer-quota-codex").Bounds()
	if panel.Bounds().Origin.Y < trigger.Origin.Y+trigger.Size.Height || m.pages.Depth() != 0 || !m.drawerOpen.GetUntracked() {
		t.Fatal("quota details should open below the trigger in the drawer")
	}
	if !app.Exists("text:81% used") || !app.Exists("text:34% used") || !app.Exists("text:Wed 14:30") {
		t.Fatal(phoneTexts(app))
	}
	captureNativePage(app, "drawer-quota-details")
	// A fresh reading updates the same card and its ring.
	changePhone(app, m, func() {
		m.State.Quotas[0].Windows = []phoneproto.QuotaWindow{{Label: "Weekly", UsedPercent: 50}}
	})
	app.WaitFor("text:50% used", time.Second)
	if app.Find("id:drawer-quota-remaining-codex").Text != "50" {
		t.Fatal("the ring did not update with the new reading")
	}
	app.Press("id:drawer-quota-" + model.AgentClaude)
	if !app.Exists("text:100% used") || app.Exists("text:50% used") {
		t.Fatal("one tap should switch the detail card to the other agent")
	}
	if m.State.Agent != model.AgentClaude {
		t.Fatal("quota inspection changed agent")
	}
	app.Back()
	if app.Exists("id:drawer-quota-details") || !m.drawerOpen.GetUntracked() {
		t.Fatal("back should dismiss the quota card before closing the drawer")
	}
	app.Press("id:drawer-quota-codex")
	changePhone(app, m, func() { m.State.Quotas = nil })
	if app.Exists("id:drawer-quotas") || app.Exists("id:drawer-quota-details") {
		t.Fatal("removed quota remained visible")
	}
}

func TestPhoneTranscriptTypographyFollowsSystemTextSize(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() {
		m.Connected, m.Error, m.Status = true, "", ""
		thread := m.Viewer.Current()
		thread.Title = "构建核对"
		thread.Messages = []model.Message{
			{ID: 101, Role: "user", TurnID: "preview", Content: "核对今天的构建结果，并整理交付说明。"},
			{ID: 102, Role: "activity", TurnID: "preview", Status: "completed", Presentation: &model.MessagePresentation{
				Title: "已核对构建", Format: "markdown", Content: "活动详情和消息正文使用相同字号。",
			}},
			{ID: 103, Role: "assistant", TurnID: "preview", Status: "completed",
				Content: "## 构建结果\n\n构建和测试均已通过。交付说明在 `README.md` 中。\n\n" +
					"- 中文和 English 在同一段内清晰可读。\n- 确认安装包签名后即可交付。\n\n" +
					"```sh\ngo test ./...\n```\n\n| 项目 | 结果 |\n| --- | --- |\n| 签名校验 | 通过 |"},
		}
		m.ShowThread(thread)
	})
	check := func(scale core.Points) {
		t.Helper()
		for _, sample := range []struct {
			selector         string
			size, lineHeight core.Points
		}{
			{"text:核对今天的构建结果", 17, 25.5},
			{"text:构建和测试均已通过", 17, 25.5},
			{"id:reply-103 >> text:构建结果", 22.95, 27.54},
			{"text:中文和 English", 17, 25.5},
			{"text:签名校验", 17, 25.5},
			{"text:README.md", 14.875, 0},
			{"text:go test ./...", 14.875, 20.825},
			{"text:已核对构建", 12, 0},
		} {
			style := app.SpanStyle(sample.selector)
			if diff := style.Font.Size - sample.size*scale; diff < -0.02 || diff > 0.02 {
				t.Errorf("%s has size %v, want %v", sample.selector, style.Font.Size, sample.size*scale)
			}
			if diff := style.LineHeight - sample.lineHeight*scale; sample.lineHeight > 0 && (diff < -0.02 || diff > 0.02) {
				t.Errorf("%s has line height %v, want %v", sample.selector, style.LineHeight, sample.lineHeight*scale)
			}
		}
	}
	check(1)
	captureNativePage(app, "phone-transcript-typography")
	app.Press("id:activity-102")
	if size := app.SpanStyle("text:活动详情和消息正文").Font.Size; size != 17 {
		t.Fatalf("activity Markdown uses %v points", size)
	}
	app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Locale: "en", FontScale: 1.4,
		SafeArea: core.Edges{Top: 47, Bottom: 34}})
	app.Settle()
	check(1.4)
}

func TestMigratedTableAndTranscriptScrollKeepConversation(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	table := "| First column with a long heading | Second column with a long heading | Last column |\n|---|---|---|\n| First value | Second value | Last value |\n\nTable notes."
	changePhone(app, m, func() {
		th := m.Viewer.Current()
		th.Messages = []model.Message{{ID: 100, Role: "assistant", Content: table, Status: "completed"}}
		m.ShowThread(th)
		m.Draft = "keep draft"
	})
	before := app.Find("id:mobile-composer").ID
	first := app.Find("text:First value")
	var horizontal *nsgotest.View
	for p := first; p != nil; p = p.Parent {
		if p.Scroll.Horizontal {
			horizontal = p
			break
		}
	}
	if horizontal == nil {
		t.Fatal("wide markdown table has no independent horizontal scroll")
	}
	app.Scroll("text:First value", 240)
	if horizontal.Offset.X <= 0 {
		t.Fatal("table did not scroll")
	}
	app.Scroll("text:Second value", -240)
	if horizontal.Offset.X != 0 {
		t.Fatal("table did not scroll back")
	}
	changePhone(app, m, func() {
		th := m.Viewer.Current()
		for i := 101; i < 140; i++ {
			th.Messages = append(th.Messages, model.Message{ID: i, Role: "user", Content: fmt.Sprint("Message ", i)})
		}
		m.ShowThread(th)
	})
	app.ScrollTo("id:chat-transcript", core.Point{Y: 100})
	if app.Find("id:chat-transcript").Offset.Y <= 0 {
		t.Fatal("transcript did not scroll vertically")
	}
	if m.Open != 7 || m.Draft != "keep draft" || app.Find("id:mobile-composer").ID != before {
		t.Fatal("scrolling navigated or rebuilt the editor")
	}
}
