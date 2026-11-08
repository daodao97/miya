package desktopui

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/agent/login"
	"miya/internal/widgets"
)

type AccountLoginUI struct {
	State        login.State
	Draft, Error string
	Busy         bool
	timer        *time.Timer
}

func (s *accountView) loginViews(c *ui.Context) {
	app := s.app
	cl, remote := app.C.State().Remote, app.C.RemoteHost()
	if s.logins == nil || cl != s.loginClient || remote != s.loginRemote {
		// Never send a code to a different machine after a tab/connection switch.
		s.stopLogins()
		s.loginClient, s.loginRemote = cl, remote
		s.loginDone = make(chan struct{})
		s.loginCall = app.C.AccountLoginTarget()
		s.logins = make(map[string]*AccountLoginUI)
		for _, provider := range agentmeta.SignIn() {
			s.logins[provider.UsageID] = &AccountLoginUI{}
		}
	}
	title, help := tr("Accounts on this computer"), tr("Sign in with the official CLI. Credentials are saved on this computer.")
	if remote {
		title, help = tr("Accounts on the connected server"), tr("Authorize here to sign in on the connected server. Credentials stay on that server.")
	}
	widgets.SettingsHeader(c, title, help, nil)
	for _, provider := range agentmeta.SignIn() {
		agent := provider.UsageID
		NativeLoginCard(c.Key("login-"+agent), agent, s.logins[agent], func(action string) { s.loginAction(agent, action) })
	}
}

func (s *accountView) stopLogins() {
	if s.loginDone != nil {
		close(s.loginDone)
		s.loginDone = nil
	}
	for agent, state := range s.logins {
		if state.timer != nil {
			state.timer.Stop()
		}
		state.Draft = ""
		if state.State.Active() && s.loginCall != nil {
			call, req := s.loginCall, login.Request{Action: "cancel", Agent: agent, SessionID: state.State.SessionID}
			go func() { _, _ = call(req) }()
		}
	}
	s.logins = nil
}

func (s *accountView) loginAction(agent, action string) {
	if s.app.C.State().Remote != s.loginClient || s.app.C.RemoteHost() != s.loginRemote {
		s.stopLogins()
		return
	}
	state := s.logins[agent]
	if state == nil || state.Busy {
		return
	}
	if state.timer != nil {
		state.timer.Stop()
	}
	req := login.Request{Agent: agent, Action: action, SessionID: state.State.SessionID, Method: agentmeta.UsageFor(agent).LoginMethod}
	if action == "submit" {
		req.Code, state.Draft = strings.TrimSpace(state.Draft), ""
	}
	state.Busy, state.Error = true, ""
	call := s.app.C.AccountLoginTarget()
	s.loginCall = call
	done := s.loginDone
	go func() {
		result, err := call(req)
		select {
		case <-done:
			if result.Active() {
				_, _ = call(login.Request{Action: "cancel", Agent: agent, SessionID: result.SessionID})
			}
			return
		default:
		}
		s.app.C.Dispatch(func() {
			// A delayed start response must not leave an abandoned native login.
			if s.logins[agent] != state || s.app.C.State().Remote != s.loginClient || s.app.C.RemoteHost() != s.loginRemote {
				if result.Active() {
					go func() { _, _ = call(login.Request{Action: "cancel", Agent: agent, SessionID: result.SessionID}) }()
				}
				return
			}
			state.Busy = false
			if err != nil {
				state.Error = err.Error()
				if action == "cancel" {
					state.State.Phase, state.State.Challenge, state.Draft = "cancelled", login.Challenge{}, ""
				}
				if !state.State.ExpiresAt.IsZero() && time.Now().After(state.State.ExpiresAt) {
					state.State.Phase, state.State.Challenge = "failed", login.Challenge{}
					state.State.Error = "Sign-in expired. Generate a new link."
				}
			} else {
				completed := state.State.Phase != "succeeded" && result.Phase == "succeeded"
				if result.Identity == nil {
					result.Identity = state.State.Identity
				}
				state.State = result
				if !result.Active() {
					state.Draft = ""
				}
				if completed {
					s.app.C.RefreshAccount()
					s.app.refreshUsage()
				}
			}
			if state.State.Active() {
				state.timer = time.AfterFunc(2*time.Second, func() {
					s.app.C.Dispatch(func() {
						if s.logins[agent] == state {
							s.loginAction(agent, "status")
						}
					})
				})
			}
		})
	}()
}

// NativeLoginCard is also rendered by UI tests for both hosts and themes.
func NativeLoginCard(c *ui.Context, agent string, s *AccountLoginUI, action func(string)) {
	provider := agentmeta.UsageFor(agent)
	name := provider.Name
	st := s.State
	widgets.SettingsCard(c).Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			widgets.AgentIcon(c, agentmeta.UsageFor(agent).ID, 18)
			ui.Text(c, name).Grow(1).FontSize(14).FontWeight(600)
			if widgets.SmallButton(c.Key("check"), tr("Check sign-in status"), s.Busy).Label(fmt.Sprintf(tr("Check %s sign-in"), name)).Clicked() {
				action("check")
			}
		})
		text := tr("Sign-in status not checked")
		if s.Busy && st.SessionID == "" {
			text = tr("Checking sign-in status…")
		} else if id := st.Identity; id != nil {
			text = tr("Not signed in")
			if id.LoggedIn {
				text = tr("Signed in")
				for _, detail := range []string{id.Email, id.Plan, id.Method} {
					if detail != "" {
						text += " · " + detail
					}
				}
			}
		}
		ui.Text(c, text).FillWidth().FontSize(12).TextColor(widgets.Muted).LineHeight(1.4)
		if !st.Active() {
			label := tr("Generate authorization link")
			if provider.LoginMethod == "device" {
				label = tr("Generate device code")
			}
			if ui.Button(c.Key("start"), label).Label(fmt.Sprintf(tr("Sign in to %s"), name)).Disabled(s.Busy).Clicked() {
				action("start")
			}
		} else {
			if st.URL != "" {
				if provider.LoginMethod == "device" {
					ui.Text(c, st.UserCode).Font(widgets.MonoFont).FontSize(22).FontWeight(600)
					ui.Text(c, tr("Open the official authorization page and enter this device code. Sign-in completes automatically here.")).FillWidth().FontSize(12).TextColor(widgets.Muted).LineHeight(1.4)
				} else {
					ui.Text(c, tr("Open the official authorization page, then paste the complete code shown there, including #state.")).FillWidth().FontSize(12).TextColor(widgets.Muted).LineHeight(1.4)
				}
				ui.Text(c, st.URL).FillWidth().FontSize(11).TextColor(widgets.Muted).SingleLine().Ellipsis("…").Tooltip(st.URL)
				ui.Row(c).Wrap().Gap(8).Children(func() {
					if widgets.SmallButton(c.Key("open"), tr("Open authorization page"), false).Clicked() {
						go mygo.Shell.OpenExternal(st.URL)
					}
					if widgets.SmallButton(c.Key("copy-link"), tr("Copy link"), false).Clicked() {
						c.WriteClipboard(st.URL)
					}
					if st.UserCode != "" && widgets.SmallButton(c.Key("copy-code"), tr("Copy device code"), false).Clicked() {
						c.WriteClipboard(st.UserCode)
					}
				})
				if provider.LoginMethod == "browser" {
					ui.Row(c).FillWidth().Gap(8).Children(func() {
						in := ui.TextInput(c.Key("code"), &s.Draft).Password().Label(fmt.Sprintf(tr("%s authorization code"), name)).Placeholder(tr("Paste authorization code#state")).Grow(1)
						submitted := in.Submitted()
						clicked := ui.Button(c.Key("submit"), tr("Submit code")).Disabled(s.Busy || st.Phase != "waiting" || strings.TrimSpace(s.Draft) == "").Clicked()
						if (submitted || clicked) && !s.Busy && st.Phase == "waiting" && strings.TrimSpace(s.Draft) != "" {
							action("submit")
						}
					})
				}
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				message := tr("Waiting for authorization…")
				if st.Phase == "starting" {
					message = tr("Generating sign-in link…")
				}
				if st.Phase == "submitting" {
					message = tr("Completing native sign-in…")
				}
				ui.Text(c, message).Grow(1).FontSize(12).TextColor(widgets.Muted)
				if widgets.SmallButton(c.Key("cancel"), tr("Cancel sign-in"), s.Busy).Clicked() {
					action("cancel")
				}
			})
		}
		if st.Phase == "succeeded" {
			ui.Text(c, tr("Sign-in completed and verified.")).FontSize(12).TextColor(widgets.Success)
		}
		for _, message := range []string{st.Error, s.Error} {
			if message != "" {
				ui.Text(c, tr(message)).FillWidth().FontSize(12).TextColor(widgets.Danger).LineHeight(1.4)
			}
		}
	})
}
