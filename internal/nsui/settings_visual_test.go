package nsui

import (
	"fmt"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/agent/provider"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/desktop"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/notification"
	"miya/internal/nswidgets"
	svcproto "miya/internal/service/protocol"
	"miya/locales"
)

func TestMain(m *testing.M) { nsgotest.Main(m) }

type settingsVisualHost struct{ *fakeHost }

func (h settingsVisualHost) ProviderViews() []provider.View {
	return []provider.View{{ID: "example", Name: "Example provider", Models: []string{"example-model"},
		Endpoints: []provider.Endpoint{{API: provider.Responses, BaseURL: "https://example.test/v1"}}}}
}

func TestDesktopSettingsLayouts(t *testing.T) {
	for _, layout := range []struct {
		width, height core.Points
		name, locale  string
		appearance    core.Appearance
	}{{780, 700, "regular", "en", core.AppearanceLight}, {560, 480, "compact", "zh-Hans", core.AppearanceDark}} {
		t.Run(layout.name, func(t *testing.T) {
			t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
			h := settingsVisualHost{settingsHost()}
			h.permission = "on-request"
			h.phoneOK, h.phone = true, svcproto.PhoneStatus{Link: "miya://desktop-preview.example.test", Peers: 1}
			h.remoteHost = true
			h.state.Remote = settingsTestService(t, func(req svcproto.Request) (any, error) {
				switch req.Op {
				case "memoryList":
					return manager.List{ExtractorAvailable: true, Extractors: []manager.ExtractorOption{{Agent: model.AgentClaude, Available: true}, {Agent: model.AgentCodex, Available: false}}, Entries: []memory.Entry{
						{ID: "example", Kind: "fact", State: "active", Version: 1, Scope: memory.Scope{Kind: "project"}, ObservedAt: time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC), Content: "Use the release checklist before shipping changes."},
					}}, nil
				case "notificationPolicy":
					return notification.PolicyView{Quiet: notification.QuietHours{Enabled: true, Start: "22:00", End: "08:00"}}, nil
				case "automationWebhook":
					return automation.WebhookView{Enabled: true, URL: "https://example.test/hooks/miya"}, nil
				case desktop.PermissionStatusOp:
					return desktop.Permissions{Supported: true, Accessibility: desktop.PermissionDenied, ScreenRecording: desktop.PermissionGranted}, nil
				}
				return nil, nil
			})
			var usage *desktopUsage
			var scope *core.Scope
			app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
				installBridge(cx)
				cx.SetTheme(nswidgets.Theme())
				cx.SetLocale(layout.locale)
				scope = cx
				usage = usageFor(cx, h)
				return settingsContent(cx, h, usage, newSettingsService(cx, h), func() {}, false)
			}, nsgotest.Size(layout.width, layout.height), nsgotest.Offscreen())
			t.Cleanup(func() { current = nil; locales.SetCurrent("") })
			app.Post(func() { scope.SetAppearance(layout.appearance) })
			app.Settle()
			capture := func(name string) {
				t.Helper()
				app.Settle()
				if nsgotest.Target() == "macos" {
					app.Advance(250 * time.Millisecond)
					app.Screenshot(layout.name + "-" + name)
				}
			}
			captureSelect := func(id string, selected int) {
				if nsgotest.Target() != "macos" {
					return
				}
				control := "id:" + id
				option := fmt.Sprintf("%s-%d", control, selected)
				app.Press(control)
				app.WaitFor(option, time.Second)
				capture(id)
				app.Press(option)
			}
			pages := []struct{ category, control string }{
				{"general", "appearance"}, {"providers", "provider-new"}, {"account", "login-start-codex"},
				{"usage", "usage-agent"}, {"phone", "quiet-hours-save"}, {"memory", "memory-add"},
				{"webhook", "settings-webhook-copy"}, {"remote-control", "settings-permission-screen"},
			}
			for _, page := range pages {
				tab := app.Find("id:settings-nav-" + page.category)
				if x := tab.Frame.Origin.X; x < 0 || x+tab.Frame.Size.Width > layout.width {
					t.Errorf("%s tab is clipped in a %d pt window", page.category, int(layout.width))
				}
			}
			scroll := func(distance core.Points) {
				view := app.Find("id:settings-scroll")
				if view.Content.Height > view.Frame.Size.Height+1 {
					app.Scroll("id:settings-scroll", distance)
				}
			}
			for _, page := range pages {
				app.Press("id:settings-nav-" + page.category)
				app.WaitFor("id:"+page.control, time.Second)
				capture(page.category)
				switch page.category {
				case "general":
					captureSelect("interface-language", 0)
					captureSelect("appearance", 0)
					scroll(550)
					capture("general-agents")
				case "usage":
					captureSelect("usage-agent", 0)
					captureSelect("usage-icon", 0)
				case "phone":
					scroll(480)
					capture("phone-notifications")
				case "providers":
					app.Press("id:provider-new")
					if app.Exists("id:provider-new") || !app.Exists("id:provider-models") {
						t.Fatal("add provider did not replace the list with its editor")
					}
					if !app.Find("id:provider-save").Props.Disabled {
						t.Fatal("empty provider form can be submitted")
					}
					capture("provider-form")
				case "memory":
					captureSelect("memory-scope", 0)
					scroll(520)
					captureSelect("memory-extractor", 0)
					app.Press("id:settings-nav-general")
					app.Press("id:settings-nav-memory")
					app.WaitFor("id:memory-add", time.Second)
					app.Press("id:memory-add")
					app.WaitFor("id:memory-content", time.Second)
					capture("memory-form")
					captureSelect("memory-kind", 1)
					app.Press("id:memory-cancel")
					app.Press("id:memory-promote-file-example")
					scroll(230)
					captureSelect("memory-promotion-file", 0)
				}
			}
			if got := usage.category.GetUntracked(); got != "remote-control" {
				t.Fatalf("selected category = %q", got)
			}
		})
	}
}
