package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"miya/internal/codex"
	"miya/internal/textutil"

	"github.com/egoist/mygo/ui"
)

func (app *chatApp) settingsView(c *ui.Context) {
	cc := app.c
	tabs := []string{tr("General"), tr("Account"), tr("Phone"), tr("Usage"), tr("Memory")}

	ui.Column(c).FillWidth().MaxWidth(760).Gap(20).Children(func() {
		ui.Tabs(c.Key("settings-tabs"), &app.settingsTab, tabs...).AlignSelf(ui.Start)
		if app.settingsTab == 0 {
			ui.Column(c).FillWidth().Gap(28).Children(func() {
				settingsGroup(c, tr("General"), "",
					func() { languageSettings(c) },
					func() { app.appearanceSettings(c) })
				notificationSettings(c, false)
				settingsGroup(c, tr("Permissions"), "", func() {
					settingLabel(c, tr("Default permissions for new conversations"),
						tr("Change them for the current conversation from the composer."))
					permissionOptions(c.Key("default-permissions"), cc.selectedPermissionPolicy(), cc.setPermissionPolicy)
				})
				app.agentSettings(c)
				app.serviceSettings(c)
			})
		} else if app.settingsTab == 1 {
			app.account.view(c)
		} else if app.settingsTab == 4 {
			app.memoryView(c)
		} else if app.settingsTab == 3 {
			app.usageSettings(c)
		} else {
			app.phoneSettings(c)
		}
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
	ui.Column(c).FillWidth().Gap(16).Children(func() {
		settingsHeader(c, tr("Current account"), tr("Synced with Codex sign-in"), func() {
			if data.Refreshing {
				mutedText(c, tr("Updating…"))
			}
			if smallButton(c.Key("refresh"), tr("Refresh"), data.Loading || data.Refreshing || action.Busy).Clicked() {
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
	settingsCard(c).Children(func() {
		if data.Notice != "" {
			mutedText(c, data.Notice)
		}
		switch {
		case data.Loading:
			ui.Text(c, tr("Loading account and usage…")).TextColor(muted)
			return
		case data.Error != "":
			ui.Text(c, data.Error).TextColor(muted)
			return
		case data.Identity == nil:
			ui.Text(c, tr("Not signed in")).FontWeight(600)
			ui.Text(c, tr("Sign in to the Codex CLI, then refresh this page.")).TextColor(muted)
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
				ui.Text(c, tr("Updated at ")+data.Updated.Local().Format("15:04:05")).FontSize(11).TextColor(muted)
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(line)
		ui.Text(c, tr("Usage")).FontSize(12).FontWeight(600).TextColor(muted)
		if a.Type != "chatgpt" {
			ui.Text(c, tr("This sign-in method does not provide ChatGPT plan quota.")).TextColor(muted)
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
				ui.Column(c.Key(i)).Grow(1).Basis(260).MinWidth(0).Padding(14).Gap(10).Background(side).Radius(10).Children(func() {
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
						text := tr("No extra credits")
						if q.Credits.Unlimited {
							text = tr("Unlimited")
						} else if q.Credits.Balance != nil {
							text = formatCreditBalance(*q.Credits.Balance)
						} else if q.Credits.HasCredits {
							text = tr("Credits available")
						}
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Text(c, tr("Extra credits")).FontSize(11).TextColor(muted)
							ui.Text(c, text).FontSize(12).FontWeight(500)
						})
					}
				})
			}
		})
		if count == 0 && data.UsageError == "" {
			ui.Text(c, tr("No usage data available.")).TextColor(muted)
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
			ui.Text(c, fmt.Sprintf(tr("%.0f%% used · %.0f%% remaining"), used, 100-used)).FontSize(11).TextColor(muted)
		})
		progressBar(c, used, color)
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			ui.Text(c, tr("Resets: ")+time.Unix(*w.ResetsAt, 0).Local().Format("01-02 15:04")).FontSize(11).TextColor(muted)
		}
	})
}

func resetCreditCard(c *ui.Context, data accountDisplay, action accountResetAction, connected bool, ask, cancel, use func()) {
	summary := data.Limits.ResetCredits
	credit := summary.Next()
	retry := action.Attempt != nil && action.Attempt.Submitted && action.Attempt.Matches(data)
	settingsCard(c).Gap(10).Children(func() {
		ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, tr("Reset cards")).FontSize(14).FontWeight(600)
			if summary != nil {
				badge(c, fmt.Sprintf(tr("%d available"), summary.AvailableCount))
			}
			ui.Spacer(c)
			if credit != nil || retry {
				label := tr("Use a card")
				if retry {
					label = tr("Check last reset result")
				}
				disabled := !connected || action.Busy || data.Refreshing || data.UsageError != "" || data.Notice != ""
				if smallButton(c.Key("ask"), label, disabled).Clicked() {
					ask()
				}
			}
		})
		if summary == nil {
			mutedText(c, tr("Reset card data unavailable."))
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
					ui.Text(c, tr("Received ")+time.Unix(credit.GrantedAt, 0).Local().Format("2006-01-02 15:04")).FontSize(11).TextColor(muted)
				}
			})
		} else if summary.AvailableCount > 0 {
			mutedText(c, tr("Reset card details unavailable. Refresh before using."))
		}
		if action.Confirming && action.Attempt.Matches(data) {
			label := tr("Confirm")
			if action.Attempt.Submitted {
				label = tr("Retry fetching result")
			}
			if action.Busy {
				label = tr("Using card…")
			}
			who := textutil.FirstNonEmpty(action.Attempt.Account.Email, tr("Current account"))
			ui.Column(c).Padding(12).Gap(8).Background(subtle).Radius(8).Children(func() {
				ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, tr("Use a reset card?")).FontWeight(600).Grow(1)
					if smallButton(c.Key("cancel"), tr("Cancel"), action.Busy).Clicked() {
						cancel()
					}
					if smallButton(c.Key("use"), label, !connected || action.Busy || data.Refreshing).TextColor(danger).Clicked() {
						use()
					}
				})
				ui.Text(c, who+tr(" quota windows will reset immediately. This cannot be undone.")).FontSize(11).TextColor(muted)
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
			if toolButton(c.Key("close"), "x", tr("Close")).Clicked() {
				app.projectDialog = false
			}
		})
		mutedText(c, tr("Add a project name and choose its working folder."))
		ui.Field(c, tr("Project name"), func() {
			if ui.TextInput(c.Key("name"), &app.projectName).Placeholder(tr("My project")).AutoFocus().Submitted() {
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
		if app.projectBrowse {
			app.serverProjectFolders(c)
		}
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
		return "Usage"
	}
	if u.ModelContextWindow != nil && *u.ModelContextWindow > 0 {
		return fmt.Sprintf(tr("Context %.0f%%"), math.Min(100, 100*float64(u.Last.TotalTokens)/float64(*u.ModelContextWindow)))
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
	if app.settingDrafts == nil {
		app.settingDrafts = map[string]string{}
	}
	var rows []func()
	for _, b := range agentBackends {
		for _, s := range b.Settings() {
			s := s
			rows = append(rows, func() { app.agentSetting(c, s) })
		}
	}
	if len(rows) > 0 {
		settingsGroup(c, tr("Agents"), "", rows...)
	}
}

// agentSetting is one agent-declared setting: an input saved on demand.
func (app *chatApp) agentSetting(c *ui.Context, s agentSetting) {
	cc := app.c
	ui.Column(c.Key("setting:" + s.ID)).FillWidth().Gap(10).Children(func() {
		settingLabel(c, s.Title, s.Help)
		current := s.Value(cc)
		draft, ok := app.settingDrafts[s.ID]
		if !ok {
			draft = current
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("input"), &draft).Placeholder(s.Placeholder).Label(s.Title).Grow(1).Font(monoFont).FontSize(12)
			if in.Changed() {
				app.settingDrafts[s.ID] = draft
			}
			changed := strings.TrimSpace(draft) != current
			save := ui.Button(c.Key("save"), tr("Save")).Disabled(!changed).Clicked()
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
