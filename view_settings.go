package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"miya/internal/codex"

	"github.com/egoist/mygo/ui"
)

func (app *chatApp) settingsView(c *ui.Context) {
	cc := app.c
	ui.Column(c).Gap(12).Children(func() {
		ui.Tabs(c.Key("settings-tabs"), &app.settingsTab, "General", "Account", "Phone").AlignSelf(ui.Start)
		if app.settingsTab == 0 {
			card(c).Padding(24).Gap(24).Children(func() {
				app.languageSettings(c)
				app.appearanceSettings(c)
 notificationSettings(c)
				zh := cc.locale() == "zh"
				ui.Column(c).Gap(10).Children(func() {
					ui.Text(c, map[bool]string{true: "ChatGPT 应如何工作？", false: "How should ChatGPT operate?"}[zh]).FontWeight(600)
					ui.Text(c, map[bool]string{
						true:  "新对话会使用此默认设置。你也可以在输入框中为当前对话修改权限。",
						false: "New conversations use this default. Change permissions in the composer for the current conversation.",
					}[zh]).TextColor(muted).FontSize(12).LineHeight(1.5)
					permissionOptions(c.Key("default-permissions"), cc.selectedPermissionPolicy(), cc.setPermissionPolicy)
				})
				app.agentSettings(c)
			})
		} else if app.settingsTab == 1 {
			app.account.view(c)
		} else {
			app.phoneSettings(c)
		}
	})
}

// languageSettings chooses the interface language explicitly rather than
// following the system's; it is kept with the other settings.
func (app *chatApp) languageSettings(c *ui.Context) {
	cc := app.c
	zh := cc.locale() == "zh"
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, map[bool]string{true: "界面语言", false: "Interface language"}[zh]).FontWeight(600)
		ui.Text(c, map[bool]string{true: "选择界面显示语言。", false: "Choose the language of the interface."}[zh]).TextColor(muted).FontSize(12)
		ui.Row(c).Gap(8).Children(func() {
			for _, l := range []struct{ id, name string }{{"zh", "中文"}, {"en", "English"}} {
				b := quiet(c.Key(l.id)).Padding(0, 12)
				if cc.locale() == l.id {
					b.Background(selectedBg)
				}
				b.Children(func() { ui.Text(c, l.name) })
				if b.Clicked() {
					cc.setLocale(l.id)
				}
			}
		})
	})
}

// accountView shows the account the controller reads, from when the tab
// opens and whenever the connection changes.
type accountView struct {
	app   *chatApp
	ready bool
	shown bool
}

func (s *accountView) init(app *chatApp) { s.app = app }

func (s *accountView) view(c *ui.Context) {
	cc := s.app.c
	// Read again when the tab opens and when the connection changes.
	if !s.shown || s.ready != cc.ready() {
		s.shown, s.ready = true, cc.ready()
		cc.refreshAccount()
	}
	data, action := cc.accountState, cc.accountReset
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Row(c).Wrap().AlignItems(ui.Center).Grow(1).Gap(8).Children(func() {
				ui.Text(c, "当前账号").FontSize(16).FontWeight(600)
				ui.Text(c, "与 Codex 登录状态同步").FontSize(11).TextColor(muted)
			})
			if data.Refreshing {
				mutedText(c, "更新中…")
			}
			if quiet(c.Key("refresh")).Height(28).Disabled(data.Loading || data.Refreshing || action.Busy).Children(func() { ui.Text(c, "刷新") }).Clicked() {
				cc.refreshAccount()
			}
		})
		accountDetails(c, data)
		if data.Identity != nil && data.Identity.Type == "chatgpt" {
			resetCreditCard(c, data, action, cc.ready(), cc.askAccountReset, cc.cancelAccountReset, cc.confirmAccountReset)
		}
	})
}

func badge(c *ui.Context, value string) {
	ui.Text(c, value).FontSize(11).TextColor(muted).Padding(3, 6).Background(bubble).Radius(4)
}

func accountDetails(c *ui.Context, data accountDisplay) {
	card(c).Radius(10).Children(func() {
		if data.Notice != "" {
			mutedText(c, data.Notice)
		}
		switch {
		case data.Loading:
			ui.Text(c, "正在读取账号和用量…").TextColor(muted)
			return
		case data.Error != "":
			ui.Text(c, data.Error).TextColor(muted)
			return
		case data.Identity == nil:
			ui.Text(c, "尚未登录").FontWeight(600)
			ui.Text(c, "请先在 Codex CLI 中登录，再刷新此页面。").TextColor(muted)
			return
		}
		a := data.Identity
		kind := "ChatGPT"
		if a.Type == "apiKey" {
			kind = "API Key"
		} else if a.Type != "chatgpt" {
			kind = a.Type
		}
		ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Row(c).Wrap().AlignItems(ui.Center).Grow(1).MinWidth(0).Gap(8).Children(func() {
				if a.Email != "" {
					ui.Text(c, a.Email).FontSize(14).FontWeight(500)
				}
				badge(c, kind)
				if a.PlanType != "" {
					badge(c, strings.ToUpper(a.PlanType))
				}
			})
			if !data.Updated.IsZero() {
				ui.Text(c, "更新于 "+data.Updated.Local().Format("15:04:05")).FontSize(11).TextColor(muted)
			}
		})
		ui.Box(c).Height(1).Background(chosen)
		ui.Text(c, "用量").FontSize(12).FontWeight(600)
		if a.Type != "chatgpt" {
			ui.Text(c, "此登录方式不提供 ChatGPT 套餐额度。").TextColor(muted)
			return
		}
		if data.UsageError != "" {
			ui.Text(c, data.UsageError).TextColor(muted)
		}
		var snapshots []quotaSnapshot
		keys := make([]string, 0, len(data.Limits.ByID))
		for key := range data.Limits.ByID {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			q := data.Limits.ByID[key]
			if q.LimitName == "" {
				q.LimitName = key
			}
			snapshots = append(snapshots, q)
		}
		if len(snapshots) == 0 && data.Limits.RateLimits != nil {
			snapshots = append(snapshots, *data.Limits.RateLimits)
		}
		count := 0
		ui.Row(c).Wrap().Gap(12).Children(func() {
			for i, q := range snapshots {
				windows := []*quotaWindow{}
				for _, w := range []*quotaWindow{q.Primary, q.Secondary} {
					if w != nil {
						windows = append(windows, w)
					}
				}
				if len(windows) == 0 && q.Credits == nil {
					continue
				}
				count += len(windows)
				ui.Column(c.Key(i)).Grow(1).Basis(260).MinWidth(0).Padding(12).Gap(8).Background(side).Radius(8).Children(func() {
					if q.LimitName != "" {
						ui.Text(c, q.LimitName).FontSize(12).FontWeight(600)
					}
					ui.Row(c).Wrap().Gap(10).Children(func() {
						for j, w := range windows {
							quotaBar(c.Key(j), w, j)
						}
					})
					if q.Credits != nil {
						count++
						text := "无额外额度"
						if q.Credits.Unlimited {
							text = "无限额"
						} else if q.Credits.Balance != nil {
							text = *q.Credits.Balance
						} else if q.Credits.HasCredits {
							text = "有可用额度"
						}
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Text(c, "额外额度").FontSize(11).TextColor(muted)
							ui.Text(c, text).FontSize(12).FontWeight(500)
						})
					}
				})
			}
		})
		if count == 0 && data.UsageError == "" {
			ui.Text(c, "暂无可用的用量数据。").TextColor(muted)
		}
	})
}

func progressBar(c *ui.Context, used float64, color ui.Color) {
	ui.Box(c).Height(4).FillWidth().Background(chosen).Radius(2).Clip().Children(func() {
		ui.Box(c).Height(4).WidthPercent(float32(used)).Background(color).Radius(2)
	})
}

func quotaBar(c *ui.Context, w *quotaWindow, index int) {
	used := math.Max(0, math.Min(100, w.UsedPercent))
	color := ink
	if used >= 90 {
		color = danger
	}
	ui.Column(c).Grow(1).Basis(230).MinWidth(0).Gap(4).Children(func() {
		ui.Row(c).Wrap().AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Text(c, quotaLabel(w, index)).FontSize(12).Grow(1)
			ui.Text(c, fmt.Sprintf("已用 %.0f%% · 剩余 %.0f%%", used, 100-used)).FontSize(11).TextColor(muted)
		})
		progressBar(c, used, color)
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			ui.Text(c, "重置时间："+time.Unix(*w.ResetsAt, 0).Local().Format("01-02 15:04")).FontSize(11).TextColor(muted)
		}
	})
}

func resetCreditCard(c *ui.Context, data accountDisplay, action accountResetAction, connected bool, ask, cancel, use func()) {
	summary := data.Limits.ResetCredits
	credit := summary.next()
	retry := action.Attempt != nil && action.Attempt.Submitted && action.Attempt.matches(data)
	card(c).Radius(10).Gap(8).Children(func() {
		ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "重置卡").FontWeight(600)
			if summary != nil {
				badge(c, fmt.Sprintf("可用 %d 张", summary.AvailableCount))
			}
			ui.Spacer(c)
			if credit != nil || retry {
				label := "使用一张"
				if retry {
					label = "查看上次使用结果"
				}
				disabled := !connected || action.Busy || data.Refreshing || data.UsageError != "" || data.Notice != ""
				if quiet(c.Key("ask")).Height(28).Disabled(disabled).Children(func() { ui.Text(c, label) }).Clicked() {
					ask()
				}
			}
		})
		if summary == nil {
			mutedText(c, "暂未取得重置卡数据。")
		} else if credit != nil {
			ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
				if credit.Title != "" {
					ui.Text(c, credit.Title).FontSize(12).FontWeight(500)
				}
				if credit.Description != "" {
					ui.Text(c, credit.Description).FontSize(11).TextColor(muted).Grow(1).MinWidth(0)
				}
			})
			ui.Row(c).Wrap().AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Text(c, resetExpiryText(*credit)).FontSize(11).TextColor(muted)
				if credit.GrantedAt > 0 {
					ui.Text(c, "获得于 "+time.Unix(credit.GrantedAt, 0).Local().Format("2006-01-02 15:04")).FontSize(11).TextColor(muted)
				}
			})
		} else if summary.AvailableCount > 0 {
			mutedText(c, "重置卡详情暂不可用，请刷新后使用。")
		}
		if action.Confirming && action.Attempt.matches(data) {
			label := "确认使用"
			if action.Attempt.Submitted {
				label = "重试获取结果"
			}
			if action.Busy {
				label = "正在使用…"
			}
			who := firstNonEmpty(action.Attempt.Account.Email, "当前账号")
			ui.Column(c).Padding(12).Gap(8).Background(subtle).Radius(8).Children(func() {
				ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, "确认使用一张重置卡？").FontWeight(600).Grow(1)
					if quiet(c.Key("cancel")).Height(28).Disabled(action.Busy).Children(func() { ui.Text(c, "取消") }).Clicked() {
						cancel()
					}
					if quiet(c.Key("use")).Height(28).Disabled(!connected || action.Busy || data.Refreshing).Children(func() { ui.Text(c, label) }).Clicked() {
						use()
					}
				})
				ui.Text(c, who+" 的额度窗口将立即重置。此操作无法撤销。").FontSize(11).TextColor(muted)
				ui.Text(c, resetExpiryText(action.Attempt.Credit)).FontSize(11).TextColor(muted)
			})
		}
		if action.Notice != "" {
			color := muted
			if action.Failed {
				color = danger
			}
			ui.Text(c, action.Notice).FontSize(12).TextColor(color)
		}
	})
}

// quotaView is the composer's ring of the account's short-window usage,
// with the windows in a card while the pointer rests on it.
type quotaView struct{ app *chatApp }

func (s *quotaView) init(app *chatApp) { s.app = app }

func (s *quotaView) view(c *ui.Context) {
	cc := s.app.c
	if !cc.currentBackend().Caps().Quota || !cc.ready() {
		return
	}
	windows := composerQuotaWindows(cc.quotaLimits)
	used := 0.0
	if len(windows) > 0 {
		used = windows[0].UsedPercent
	}
	c = c.Key("quota")
	trigger := ui.Box(c).Size(30, 30).Justify(ui.Center).AlignItems(ui.Center).Radius(7).Label("用量")
	if trigger.Hovered() {
		trigger.Background(hover)
	}
	trigger.Children(func() {
		ui.Box(c).Size(22, 22).Children(func() {
			ui.Image(c.Key("track"), ringSVG(100, "#d7d7d7")).Size(22, 22).Absolute().Left(0).Top(0)
			ui.Image(c.Key("used"), ringSVG(used, "#454545")).Size(22, 22).Absolute().Left(0).Top(0)
		})
	})
	ui.TooltipBase(c, trigger, func(tip ui.Element) {
		tip.Width(200).Padding(14).Gap(12).Background(canvas).Border(1, line).Radius(10).Shadow(0, 6, 20, 0, shadow).TextColor(ink)
		tip.Children(func() {
			if len(windows) == 0 {
				mutedText(c, cc.quotaStatus)
				return
			}
			for i, w := range windows {
				label := "5h"
				switch *w.WindowDurationMins {
				case 7200:
					label = "5d"
				case 10080:
					label = "7d"
				}
				pct := math.Max(0, math.Min(100, w.UsedPercent))
				ui.Column(c.Key(i)).FillWidth().Gap(6).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
						ui.Text(c, label).Grow(1).FontSize(12)
						ui.Text(c, fmt.Sprintf("已用 %.0f%%", pct)).FontSize(11).TextColor(muted)
					})
					progressBar(c, pct, ink)
				})
			}
		})
	})
}

var (
	ringMu    sync.Mutex
	ringCache = map[string]*ui.SVG{}
)

// ringSVG draws a usage ring as an SVG in its own color, kept per percent.
func ringSVG(percent float64, color string) *ui.SVG {
	key := fmt.Sprintf("%.0f %s", math.Round(percent), color)
	ringMu.Lock()
	defer ringMu.Unlock()
	if s, ok := ringCache[key]; ok {
		return s
	}
	s := ui.MustParseSVG([]byte(strings.ReplaceAll(quotaRing(math.Round(percent)), "#555555", color)))
	ringCache[key] = s
	return s
}

// Prefer a five-day bucket when supplied; otherwise show the short window first.
func composerQuotaWindows(l accountLimits) []*quotaWindow {
	q := l.RateLimits
	if v, ok := l.ByID[codexAccountLimitID]; ok {
		q = &v
	}
	if q == nil {
		return nil
	}
	var out []*quotaWindow
	for _, duration := range []int{7200, 300, 10080} {
		for _, w := range []*quotaWindow{q.Primary, q.Secondary} {
			if w != nil && w.WindowDurationMins != nil && *w.WindowDurationMins == duration {
				out = append(out, w)
			}
		}
	}
	return out
}

func quotaRing(percent float64) string {
	p := math.Max(0, math.Min(100, percent))
	angle := p * 2 * math.Pi / 100
	x, y := 12+8*math.Sin(angle), 12-8*math.Cos(angle)
	arc := ""
	if p >= 100 {
		arc = `<circle cx="12" cy="12" r="8" fill="none" stroke="#555555" stroke-width="2.5"/>`
	} else if p > 0 {
		large := 0
		if p > 50 {
			large = 1
		}
		arc = fmt.Sprintf(`<path d="M12 4 A8 8 0 %d 1 %.3f %.3f" fill="none" stroke="#555555" stroke-width="2.5" stroke-linecap="round"/>`, large, x, y)
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">` + arc + `</svg>`
}

func (app *chatApp) projectDialogView(c *ui.Context) {
	ui.DialogBase(c, &app.projectDialog, func(backdrop, box ui.Element) {
		// A light backdrop keeps the app legible behind the dialog.
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30))
		box.Width(520).MaxWidthPercent(90).Padding(24).Gap(16).Background(panel).Border(1, border).Radius(14).
			Shadow(0, 18, 48, 0, ui.RGBA(0, 0, 0, 0.18)).TextColor(ink)
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Select project")).FontSize(18).FontWeight(600).Grow(1)
			if toolButton(c.Key("close"), "x", "Close").Clicked() {
				app.projectDialog = false
			}
		})
		mutedText(c, tr("Add a project name and choose its working folder."))
		ui.Field(c, tr("Project name"), func() {
			if ui.TextInput(c.Key("name"), &app.projectName).Placeholder("My project").AutoFocus().Submitted() {
				app.confirmProject()
			}
		})
		ui.Field(c, tr("Project folder"), func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.TextInput(c.Key("path"), &app.projectPath).Placeholder("/path/to/project").Grow(1).Submitted() {
					app.confirmProject()
				}
				if ui.Button(c.Key("browse"), tr("Browse…")).Clicked() {
					app.browseProject()
				}
			})
		}).Error(app.projectError)
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
				app.projectDialog = false
			}
			if ui.PrimaryButton(c.Key("confirm"), tr("Select project")).Clicked() {
				app.confirmProject()
			}
		})
	})
}

func usageSummary(u *codex.TokenUsage) string {
	if u == nil {
		return "用量"
	}
	if u.ModelContextWindow != nil && *u.ModelContextWindow > 0 {
		return fmt.Sprintf("上下文 %.0f%%", math.Min(100, 100*float64(u.Last.TotalTokens)/float64(*u.ModelContextWindow)))
	}
	return fmt.Sprintf("%s tokens", usageTokenCount(u.Total.TotalTokens))
}

func usageTokenCount(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// agentSettings shows the settings agents declare, without knowing them.
func (app *chatApp) agentSettings(c *ui.Context) {
	cc := app.c
	if app.settingDrafts == nil {
		app.settingDrafts = map[string]string{}
	}
	for _, b := range agentBackends {
		for _, s := range b.Settings() {
			ui.Column(c.Key("setting:" + s.ID)).Gap(8).Children(func() {
				ui.Text(c, s.Title).FontWeight(600)
				if s.Help != "" {
					ui.Text(c, s.Help).TextColor(muted).FontSize(12).LineHeight(1.5)
				}
				current := s.Value(cc)
				draft, ok := app.settingDrafts[s.ID]
				if !ok {
					draft = current
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					in := ui.TextInput(c.Key("input"), &draft).Placeholder(s.Placeholder).Label(s.Title).Grow(1).Font(monoFont).FontSize(12)
					if in.Changed() {
						app.settingDrafts[s.ID] = draft
					}
					changed := strings.TrimSpace(draft) != current
					save := ui.Button(c.Key("save"), "保存").Disabled(!changed).Clicked()
					if (save || in.Submitted()) && changed {
						s.Save(cc, draft)
						delete(app.settingDrafts, s.ID)
					}
				})
				if s.Status == nil {
					return
				}
				if msg, bad := s.Status(cc); bad {
					ui.Text(c, msg).TextColor(danger).FontSize(11)
				} else if msg != "" {
					ui.Text(c, msg).TextColor(muted).FontSize(11).Font(monoFont).Selectable()
				}
			})
		}
	}
}
