package desktopui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/account"
	"miya/internal/agent"
	"miya/internal/textutil"
	"miya/internal/tokenusage"
	"miya/internal/widgets"
)

func (app *App) settingsView(c *ui.Context) {
	cc := app.C
	tabs := []string{tr("General"), tr("Account"), tr("Phone"), tr("Usage"), tr("Memory")}

	ui.Column(c).FillWidth().MaxWidth(760).Gap(20).Children(func() {
		ui.Tabs(c.Key("settings-tabs"), &app.SettingsTab, tabs...).AlignSelf(ui.Start)
		if app.SettingsTab == 0 {
			ui.Column(c).FillWidth().Gap(28).Children(func() {
				widgets.SettingsGroup(c, tr("General"), "",
					func() { widgets.LanguageSettings(c) },
					func() { app.appearanceSettings(c) })
				widgets.NotificationSettings(c, false)
				widgets.SettingsGroup(c, tr("Permissions"), "", func() {
					widgets.SettingLabel(c, tr("Default permissions for new conversations"),
						tr("Change them for the current conversation from the composer."))
					permissionOptions(c.Key("default-permissions"), cc.SelectedPermissionPolicy(), cc.State().SetPermissionPolicy)
				})
				app.agentSettings(c)
				app.ServiceSettings(c)
			})
		} else if app.SettingsTab == 1 {
			app.account.view(c)
		} else if app.SettingsTab == 4 {
			app.memoryView(c)
		} else if app.SettingsTab == 3 {
			app.usageSettings(c)
		} else {
			app.phoneSettings(c)
		}
	})
}

// accountView shows the account the controller reads, from when the tab
// opens and whenever the connection changes.
type accountView struct {
	app   *App
	ready bool
	shown bool
}

func (s *accountView) init(app *App) { s.app = app }

func (s *accountView) view(c *ui.Context) {
	cc := s.app.C
	// Read again when the tab opens and when the connection changes.
	if !s.shown || s.ready != cc.State().Ready() {
		s.shown, s.ready = true, cc.State().Ready()
		cc.RefreshAccount()
	}
	data, action := cc.State().AccountState, cc.State().AccountReset
	ui.Column(c).FillWidth().Gap(16).Children(func() {
		widgets.SettingsHeader(c, tr("Current account"), tr("Synced with Codex sign-in"), func() {
			if data.Refreshing {
				widgets.MutedText(c, tr("Updating…"))
			}
			if widgets.SmallButton(c.Key("refresh"), tr("Refresh"), data.Loading || data.Refreshing || action.Busy).Clicked() {
				cc.RefreshAccount()
			}
		})
		AccountDetails(c, data)
		if data.Identity != nil && data.Identity.Type == "chatgpt" {
			ResetCreditCard(c, data, action, cc.State().Ready(), cc.AskAccountReset, cc.CancelAccountReset, cc.ConfirmAccountReset)
		}
	})
}

func badge(c *ui.Context, value string) {
	ui.Text(c, value).FontSize(11).TextColor(widgets.Muted).Padding(3, 6).Background(widgets.Bubble).Radius(4)
}

func AccountDetails(c *ui.Context, data account.Display) {
	widgets.SettingsCard(c).Children(func() {
		if data.Notice != "" {
			widgets.MutedText(c, data.Notice)
		}
		switch {
		case data.Loading:
			ui.Text(c, tr("Loading account and usage…")).TextColor(widgets.Muted)
			return
		case data.Error != "":
			ui.Text(c, data.Error).TextColor(widgets.Muted)
			return
		case data.Identity == nil:
			ui.Text(c, tr("Not signed in")).FontWeight(600)
			ui.Text(c, tr("Sign in to the Codex CLI, then refresh this page.")).TextColor(widgets.Muted)
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
				ui.Text(c, tr("Updated at ")+data.Updated.Local().Format("15:04:05")).FontSize(11).TextColor(widgets.Muted)
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(widgets.Line)
		ui.Text(c, tr("Usage")).FontSize(12).FontWeight(600).TextColor(widgets.Muted)
		if a.Type != "chatgpt" {
			ui.Text(c, tr("This sign-in method does not provide ChatGPT plan quota.")).TextColor(widgets.Muted)
			return
		}
		if data.UsageError != "" {
			ui.Text(c, data.UsageError).TextColor(widgets.Muted)
		}
		var snapshots []account.QuotaSnapshot
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
				windows := []*account.QuotaWindow{}
				for _, w := range []*account.QuotaWindow{q.Primary, q.Secondary} {
					if w != nil {
						windows = append(windows, w)
					}
				}
				if len(windows) == 0 && q.Credits == nil {
					continue
				}
				count += len(windows)
				ui.Column(c.Key(i)).Grow(1).Basis(260).MinWidth(0).Padding(14).Gap(10).Background(widgets.Side).Radius(10).Children(func() {
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
							text = account.FormatBalance(*q.Credits.Balance)
						} else if q.Credits.HasCredits {
							text = tr("Credits available")
						}
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Text(c, tr("Extra credits")).FontSize(11).TextColor(widgets.Muted)
							ui.Text(c, text).FontSize(12).FontWeight(500)
						})
					}
				})
			}
		})
		if count == 0 && data.UsageError == "" {
			ui.Text(c, tr("No usage data available.")).TextColor(widgets.Muted)
		}
	})
}

func progressBar(c *ui.Context, used float64, color ui.Color) {
	ui.Box(c).Height(4).FillWidth().Background(widgets.Chosen).Radius(2).Clip().Children(func() {
		ui.Box(c).Height(4).WidthPercent(float32(used)).Background(color).Radius(2)
	})
}

func quotaBar(c *ui.Context, w *account.QuotaWindow, index int) {
	used := math.Max(0, math.Min(100, w.UsedPercent))
	color := widgets.Ink
	if used >= 90 {
		color = widgets.Danger
	}
	ui.Column(c).Grow(1).Basis(230).MinWidth(0).Gap(4).Children(func() {
		ui.Row(c).Wrap().AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Text(c, account.QuotaLabel(w, index)).FontSize(12).Grow(1)
			ui.Text(c, fmt.Sprintf(tr("%.0f%% used · %.0f%% remaining"), used, 100-used)).FontSize(11).TextColor(widgets.Muted)
		})
		progressBar(c, used, color)
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			ui.Text(c, tr("Resets: ")+time.Unix(*w.ResetsAt, 0).Local().Format("01-02 15:04")).FontSize(11).TextColor(widgets.Muted)
		}
	})
}

func ResetCreditCard(c *ui.Context, data account.Display, action account.ResetAction, connected bool, ask, cancel, use func()) {
	summary := data.Limits.ResetCredits
	credit := summary.Next()
	retry := action.Attempt != nil && action.Attempt.Submitted && action.Attempt.Matches(data)
	widgets.SettingsCard(c).Gap(10).Children(func() {
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
				if widgets.SmallButton(c.Key("ask"), label, disabled).Clicked() {
					ask()
				}
			}
		})
		if summary == nil {
			widgets.MutedText(c, tr("Reset card data unavailable."))
		} else if credit != nil {
			ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
				if credit.Title != "" {
					ui.Text(c, credit.Title).FontSize(12).FontWeight(500)
				}
				if credit.Description != "" {
					ui.Text(c, credit.Description).FontSize(11).TextColor(widgets.Muted).Grow(1).MinWidth(0)
				}
			})
			ui.Row(c).Wrap().AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Text(c, account.ResetExpiryText(*credit)).FontSize(11).TextColor(widgets.Muted)
				if credit.GrantedAt > 0 {
					ui.Text(c, tr("Received ")+time.Unix(credit.GrantedAt, 0).Local().Format("2006-01-02 15:04")).FontSize(11).TextColor(widgets.Muted)
				}
			})
		} else if summary.AvailableCount > 0 {
			widgets.MutedText(c, tr("Reset card details unavailable. Refresh before using."))
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
			ui.Column(c).Padding(12).Gap(8).Background(widgets.Subtle).Radius(8).Children(func() {
				ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, tr("Use a reset card?")).FontWeight(600).Grow(1)
					if widgets.SmallButton(c.Key("cancel"), tr("Cancel"), action.Busy).Clicked() {
						cancel()
					}
					if widgets.SmallButton(c.Key("use"), label, !connected || action.Busy || data.Refreshing).TextColor(widgets.Danger).Clicked() {
						use()
					}
				})
				ui.Text(c, who+tr(" quota windows will reset immediately. This cannot be undone.")).FontSize(11).TextColor(widgets.Muted)
				ui.Text(c, account.ResetExpiryText(action.Attempt.Credit)).FontSize(11).TextColor(widgets.Muted)
			})
		}
		if action.Notice != "" {
			color := widgets.Muted
			if action.Failed {
				color = widgets.Danger
			}
			ui.Text(c, action.Notice).FontSize(12).TextColor(color)
		}
	})
}

func (app *App) projectDialogView(c *ui.Context) {
	ui.DialogBase(c, &app.ProjectDialog, func(backdrop, box ui.Element) {
		// A light backdrop keeps the app legible behind the dialog.
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30))
		box.Width(520).MaxWidthPercent(90).Padding(24).Gap(16).Background(widgets.Panel).Border(1, widgets.Border).Radius(14).
			Shadow(0, 18, 48, 0, ui.RGBA(0, 0, 0, 0.18)).TextColor(widgets.Ink)
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Select project")).FontSize(18).FontWeight(600).Grow(1)
			if widgets.ToolButton(c.Key("close"), "x", tr("Close")).Clicked() {
				app.ProjectDialog = false
			}
		})
		widgets.MutedText(c, tr("Add a project name and choose its working folder."))
		ui.Field(c, tr("Project name"), func() {
			if ui.TextInput(c.Key("name"), &app.ProjectName).Placeholder(tr("My project")).AutoFocus().Submitted() {
				app.confirmProject()
			}
		})
		ui.Field(c, tr("Project folder"), func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.TextInput(c.Key("path"), &app.ProjectPath).Placeholder("/path/to/project").Grow(1).Submitted() {
					app.confirmProject()
				}
				if ui.Button(c.Key("browse"), tr("Browse…")).Clicked() {
					app.browseProject()
				}
			})
		}).Error(app.ProjectError)
		if app.projectBrowse {
			app.serverProjectFolders(c)
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
				app.ProjectDialog = false
			}
			if ui.PrimaryButton(c.Key("confirm"), tr("Select project")).Clicked() {
				app.confirmProject()
			}
		})
	})
}

func UsageSummary(u *tokenusage.Usage) string {
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
func (app *App) agentSettings(c *ui.Context) {
	if app.settingDrafts == nil {
		app.settingDrafts = map[string]string{}
	}
	var rows []func()
	for _, s := range app.C.AgentSettings() {
		s := s
		rows = append(rows, func() { app.agentSetting(c, s) })
	}

	if len(rows) > 0 {
		widgets.SettingsGroup(c, tr("Agents"), "", rows...)
	}
}

// agentSetting is one agent-declared setting: an input saved on demand.
func (app *App) agentSetting(c *ui.Context, s agent.Setting) {
	ui.Column(c.Key("setting:" + s.ID)).FillWidth().Gap(10).Children(func() {
		widgets.SettingLabel(c, s.Title, s.Help)
		current := s.Value()
		draft, ok := app.settingDrafts[s.ID]
		if !ok {
			draft = current
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("input"), &draft).Placeholder(s.Placeholder).Label(s.Title).Grow(1).Font(widgets.MonoFont).FontSize(12)
			if in.Changed() {
				app.settingDrafts[s.ID] = draft
			}
			changed := strings.TrimSpace(draft) != current
			save := ui.Button(c.Key("save"), tr("Save")).Disabled(!changed).Clicked()
			if (save || in.Submitted()) && changed {
				s.Save(draft)
				delete(app.settingDrafts, s.ID)
			}
		})
		if s.Status == nil {
			return
		}
		if msg, bad := s.Status(); bad {
			ui.Text(c, msg).TextColor(widgets.Danger).FontSize(11)
		} else if msg != "" {
			ui.Text(c, msg).TextColor(widgets.Muted).FontSize(11).Font(widgets.MonoFont).Selectable()
		}
	})
}
