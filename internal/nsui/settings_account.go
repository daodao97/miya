package nsui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/agent/account"
	agentmeta "miya/internal/agent/catalog"
	"miya/internal/agent/login"
	"miya/internal/agent/quota"
	"miya/internal/nswidgets"
	svcclient "miya/internal/service/client"
)

type settingsAccount struct {
	cx       *core.Scope
	c        Host
	revision *core.Signal[int]
	logins   map[string]*settingsLogin
	client   *svcclient.Client
	remote   bool
	usage    *desktopUsage
}
type settingsLogin struct {
	state          login.State
	draft          *core.Signal[string]
	busy           bool
	err            string
	stop           func() bool
	call           func(login.Request) (login.State, error)
	closed         atomic.Bool
	mu             sync.Mutex
	pendingSession string
}

func newSettingsAccount(cx *core.Scope, c Host, category *core.Signal[string]) *settingsAccount {
	a := &settingsAccount{cx: cx, c: c, revision: core.NewSignal(cx, 0), logins: map[string]*settingsLogin{}, usage: usageFor(cx, c)}
	cx.Effect(func() {
		cl := Live(func() *svcclient.Client { return c.State().Remote })()
		remote := Live(c.RemoteHost)()
		cx.Untrack(func() {
			if a.client != cl || a.remote != remote {
				a.stopLogins()
				a.client, a.remote = cl, remote
				a.changed()
			}
		})
	})
	refreshKey := core.NewMemo(cx, Live(func() struct {
		selected string
		ready    bool
		client   *svcclient.Client
	} {
		return struct {
			selected string
			ready    bool
			client   *svcclient.Client
		}{category.Get(), c.State().Ready != nil && c.State().Ready(), c.State().Remote}
	}))
	cx.Effect(func() {
		key := refreshKey.Get()
		selected := key.selected
		cx.Untrack(func() {
			if selected == "account" {
				c.RefreshAccount()
				a.refreshUsage()
			}
		})
	})
	cx.Cleanup(a.stopLogins)
	return a
}
func (a *settingsAccount) changed() { a.revision.Update(func(n int) int { return n + 1 }) }
func (a *settingsAccount) stopLogins() {
	for id, s := range a.logins {
		s.mu.Lock()
		s.closed.Store(true)
		pending := s.pendingSession
		s.mu.Unlock()
		if pending != "" && s.call != nil {
			go cancelSettingsLogin(s.call, id, pending)
		}
		if s.stop != nil {
			s.stop()
		}
		if s.state.Active() && s.call != nil && s.state.SessionID != pending {
			go cancelSettingsLogin(s.call, id, s.state.SessionID)
		}
	}
	a.logins = map[string]*settingsLogin{}
}
func cancelSettingsLogin(call func(login.Request) (login.State, error), id, session string) {
	_, _ = call(login.Request{Agent: id, Action: "cancel", SessionID: session})
}
func (a *settingsAccount) login(id string) *settingsLogin {
	if s := a.logins[id]; s != nil {
		return s
	}
	s := &settingsLogin{draft: core.NewSignal(a.cx, ""), call: a.c.AccountLoginTarget()}
	a.logins[id] = s
	return s
}
func (a *settingsAccount) loginAction(id, action string) {
	s := a.login(id)
	if s.busy || s.closed.Load() {
		return
	}
	if a.client != a.c.State().Remote || a.remote != a.c.RemoteHost() {
		a.stopLogins()
		a.changed()
		return
	}
	if s.call == nil {
		return
	}
	if s.stop != nil {
		s.stop()
	}
	req := login.Request{Agent: id, Action: action, SessionID: s.state.SessionID, Method: agentmeta.UsageFor(id).LoginMethod}
	if action == "submit" {
		req.Code = strings.TrimSpace(s.draft.Get())
		s.draft.Set("")
	}
	s.busy, s.err = true, ""
	a.changed()
	call := s.call
	go func() {
		result, err := call(req)
		// A response may arrive after closing the dialog, when Scope.Post drops
		// callbacks. Cancel that native flow here as well as in the UI callback.
		s.mu.Lock()
		closed := s.closed.Load()
		if result.Active() && !closed {
			s.pendingSession = result.SessionID
		}
		s.mu.Unlock()
		if closed {
			if result.Active() {
				cancelSettingsLogin(call, id, result.SessionID)
			}
			return
		}
		a.cx.Post(func() {
			if s.closed.Load() || a.logins[id] != s || a.client != a.c.State().Remote || a.remote != a.c.RemoteHost() {
				if result.Active() {
					go cancelSettingsLogin(call, id, result.SessionID)
				}
				return
			}
			s.mu.Lock()
			s.pendingSession = ""
			s.mu.Unlock()
			s.busy = false
			if err != nil {
				s.err = err.Error()
				if action == "cancel" {
					s.state.Phase = "cancelled"
					s.state.Challenge = login.Challenge{}
				}
				if !s.state.ExpiresAt.IsZero() && time.Now().After(s.state.ExpiresAt) {
					s.state.Phase = "failed"
					s.state.Challenge = login.Challenge{}
					s.state.Error = tr("Sign-in expired. Generate a new link.")
				}
			} else {
				completed := s.state.Phase != "succeeded" && result.Phase == "succeeded"
				if result.Identity == nil {
					result.Identity = s.state.Identity
				}
				s.state = result
				if !result.Active() {
					s.draft.Set("")
				}
				if completed {
					a.c.RefreshAccount()
					a.refreshUsage()
				}
			}
			a.changed()
			if s.state.Active() {
				s.stop = a.cx.AfterFunc(2*time.Second, func() { a.loginAction(id, "status") })
			}
		})
	}()
}
func (a *settingsAccount) refreshUsage() { a.usage.refresh() }
func (a *settingsAccount) groups() []ui.SettingsGroup {
	title := tr("Accounts on this computer")
	help := tr("Sign in with the official CLI. Credentials are saved on this computer.")
	// Header text follows connection changes without reconstructing login state.
	header := settingsText(a.cx, Live(func() string {
		if a.c.RemoteHost() {
			return tr("Accounts on the connected server") + "\n" + tr("Authorize here to sign in on the connected server. Credentials stay on that server.")
		}
		return title + "\n" + help
	}))
	group := ui.SettingsGroup{ID: "sign-in", Title: tr("Account"), Items: []ui.SettingsItem{{ID: "target", Content: header}}}
	for _, p := range agentmeta.SignIn() {
		group.Items = append(group.Items, ui.SettingsItem{ID: "login-" + p.UsageID, Title: p.Name, Content: a.loginCard(p)})
	}
	return []ui.SettingsGroup{group, {ID: "subscriptions", Title: tr("Usage"), Actions: []core.Node{settingsButton("refresh-account", tr("Refresh"), func() { a.c.RefreshAccount(); a.refreshUsage() }, Live(func() bool {
		a.usage.revision.Get()
		d := a.c.State().AccountState
		return a.usage.busy || d.Loading || d.Refreshing || a.c.State().AccountReset.Busy
	}))}, Items: []ui.SettingsItem{{ID: "subscriptions", Title: tr("Codex account"), Content: a.subscriptionView()}}}}
}
func (a *settingsAccount) loginCard(p agentmeta.Descriptor) core.Node {
	key := core.NewMemo(a.cx, func() string {
		a.revision.Get()
		s := a.login(p.UsageID)
		data, _ := json.Marshal(s.state)
		// A status poll only changes Busy; the input and caret stay mounted while
		// waiting. Actual challenge/identity/error changes rebuild the card.
		return string(data) + s.err + fmt.Sprint(s.busy && s.state.SessionID == "")
	})
	return core.Dynamic(func(cx *core.Scope) core.Node {
		key.Get()
		s := a.login(p.UsageID)
		st := s.state
		action := func(op string) func() { return func() { a.loginAction(p.UsageID, op) } }
		disabled := func() bool { a.revision.Get(); return s.busy }
		nodes := []core.Node{settingsText(cx, func() string { return p.Name }), settingsButton("login-check-"+p.UsageID, tr("Check sign-in status"), action("check"), disabled)}
		text := tr("Sign-in status not checked")
		if s.busy && st.SessionID == "" {
			text = tr("Checking sign-in status…")
		} else if id := st.Identity; id != nil {
			text = tr("Not signed in")
			if id.LoggedIn {
				text = tr("Signed in")
				for _, v := range []string{id.Email, id.Plan, id.Method} {
					if v != "" {
						text += " · " + v
					}
				}
			}
		}
		nodes = append(nodes, settingsText(cx, func() string { return text }))
		if !st.Active() {
			label := tr("Generate authorization link")
			if p.LoginMethod == "device" {
				label = tr("Generate device code")
			}
			nodes = append(nodes, settingsButton("login-start-"+p.UsageID, label, action("start"), disabled))
		} else {
			if st.URL != "" {
				detail := tr("Open the official authorization page, then paste the complete code shown there, including #state.")
				if p.LoginMethod == "device" {
					detail = st.UserCode + "\n" + tr("Open the official authorization page and enter this device code. Sign-in completes automatically here.")
				}
				nodes = append(nodes, settingsText(cx, func() string { return detail + "\n" + st.URL }), settingsActions(settingsButton("login-open-"+p.UsageID, tr("Open authorization page"), func() { cx.OpenURL(st.URL, nil) }, nil), settingsButton("login-copy-link-"+p.UsageID, tr("Copy link"), func() { cx.SetClipboard(st.URL) }, nil)))
				if st.UserCode != "" {
					nodes = append(nodes, settingsButton("login-copy-code-"+p.UsageID, tr("Copy device code"), func() { cx.SetClipboard(st.UserCode) }, nil))
				}
				if p.LoginMethod == "browser" {
					submit := func() {
						if !s.busy && s.state.Phase == "waiting" && strings.TrimSpace(s.draft.Get()) != "" {
							a.loginAction(p.UsageID, "submit")
						}
					}
					nodes = append(nodes, settingsActions(ui.Input{TestID: "login-code-" + p.UsageID, Label: fmt.Sprintf(tr("%s authorization code"), p.Name), Placeholder: tr("Paste authorization code#state"), Masked: true, Value: s.draft.Get, OnChange: s.draft.Set, OnSubmit: func(string) { submit() }, Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(150)}}, settingsButton("login-submit-"+p.UsageID, tr("Submit code"), submit, func() bool { return disabled() || s.state.Phase != "waiting" || strings.TrimSpace(s.draft.Get()) == "" })))
				}
			}
			message := tr("Waiting for authorization…")
			if st.Phase == "starting" {
				message = tr("Generating sign-in link…")
			}
			if st.Phase == "submitting" {
				message = tr("Completing native sign-in…")
			}
			nodes = append(nodes, settingsText(cx, func() string { return message }), settingsButton("login-cancel-"+p.UsageID, tr("Cancel sign-in"), action("cancel"), disabled))
		}
		if st.Phase == "succeeded" {
			nodes = append(nodes, settingsText(cx, func() string { return tr("Sign-in completed and verified.") }))
		}
		for _, msg := range []string{st.Error, s.err} {
			if msg != "" {
				nodes = append(nodes, settingsText(cx, func() string { return tr(msg) }))
			}
		}
		return settingsColumn(nodes...)
	})
}
func (a *settingsAccount) subscriptionView() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		a.revision.Get()
		d := Live(func() account.Display { return a.c.State().AccountState })()
		var nodes []core.Node
		add := func(s string) {
			if s != "" {
				nodes = append(nodes, settingsText(cx, func() string { return s }))
			}
		}
		title := tr("Local subscriptions")
		help := tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on this computer.")
		if a.c.RemoteHost() {
			title = tr("Server subscriptions")
			help = tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on the server.")
		}
		add(title)
		add(help)
		add(d.Notice)
		switch {
		case d.Loading:
			add(tr("Loading account and usage…"))
		case d.Error != "":
			add(d.Error)
		case d.Identity == nil:
			add(tr("Not signed in"))
			add(tr("Use the sign-in controls above to connect your Codex account."))
		default:
			id := d.Identity
			add(id.Email + " · " + id.Type + " · " + strings.ToUpper(id.PlanType))
			if !d.Updated.IsZero() {
				add(tr("Updated at ") + d.Updated.Local().Format("15:04:05"))
			}
			if id.Type != "chatgpt" {
				add(tr("This sign-in method does not provide ChatGPT plan quota."))
			} else {
				add(d.UsageError)
				keys := make([]string, 0, len(d.Limits.ByID))
				for k := range d.Limits.ByID {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				var snapshots []account.QuotaSnapshot
				for _, k := range keys {
					q := d.Limits.ByID[k]
					if q.LimitName == "" {
						q.LimitName = k
					}
					snapshots = append(snapshots, q)
				}
				if len(snapshots) == 0 && d.Limits.RateLimits != nil {
					snapshots = append(snapshots, *d.Limits.RateLimits)
				}
				count := 0
				for _, q := range snapshots {
					add(q.LimitName)
					for i, w := range []*account.QuotaWindow{q.Primary, q.Secondary} {
						if w == nil {
							continue
						}
						count++
						nodes = append(nodes, settingsQuota(cx, account.QuotaLabel(w, i), w.UsedPercent))
						if w.ResetsAt != nil && *w.ResetsAt > 0 {
							add(tr("Resets: ") + time.Unix(*w.ResetsAt, 0).Local().Format("01-02 15:04"))
						}
					}
					if cr := q.Credits; cr != nil {
						count++
						value := tr("No extra credits")
						if cr.Unlimited {
							value = tr("Unlimited")
						} else if cr.Balance != nil {
							value = account.FormatBalance(*cr.Balance)
						} else if cr.HasCredits {
							value = tr("Credits available")
						}
						add(tr("Extra credits") + ": " + value)
					}
				}
				if count == 0 && d.UsageError == "" {
					add(tr("No usage data available."))
				}
				nodes = append(nodes, a.resetCard(cx, d))
			}
		}
		a.usage.revision.Get()
		for _, provider := range agentmeta.Usage() {
			if provider.UsageReset && d.Identity != nil {
				continue
			}
			nodes = append(nodes, a.usage.subscriptionCard(cx, provider.UsageID))
		}
		return settingsColumn(nodes...)
	})
}
func settingsQuota(cx *core.Scope, label string, used float64) core.Node {
	used = quota.Percent(used)
	colors := nswidgets.ColorsOf(cx)
	return settingsColumn(settingsText(cx, func() string {
		return label + " · " + fmt.Sprintf(tr("%.0f%% used · %.0f%% remaining"), used, 100-used)
	}), core.View{Style: core.Style{Height: core.Pt(4), Width: core.Pct(100), CornerRadius: 2}, StyleFunc: func() core.Style { return core.Style{Background: colors.Chosen()} }, Children: []core.Node{core.View{Style: core.Style{Height: core.Pt(4), Width: core.Pct(float32(used)), CornerRadius: 2}, StyleFunc: func() core.Style {
		color := colors.Ink()
		if used >= 90 {
			color = colors.Danger()
		}
		return core.Style{Background: color}
	}}}})
}
func (a *settingsAccount) resetCard(cx *core.Scope, d account.Display) core.Node {
	action := a.c.State().AccountReset
	summary := d.Limits.ResetCredits
	credit := summary.Next()
	retry := action.Attempt != nil && action.Attempt.Submitted && action.Attempt.Matches(d)
	ready := func() bool { return a.c.State().Ready != nil && a.c.State().Ready() }
	var nodes []core.Node
	add := func(s string) { nodes = append(nodes, settingsText(cx, func() string { return s })) }
	add(tr("Reset cards"))
	if summary == nil {
		add(tr("Reset card data unavailable."))
	} else {
		add(fmt.Sprintf(tr("%d available"), summary.AvailableCount))
	}
	if credit != nil {
		add(credit.Title + "\n" + credit.Description + "\n" + account.ResetExpiryText(*credit))
	} else if summary != nil && summary.AvailableCount > 0 {
		add(tr("Reset card details unavailable. Refresh before using."))
	}
	if credit != nil || retry {
		label := tr("Use a card")
		if retry {
			label = tr("Check last reset result")
		}
		nodes = append(nodes, settingsButton("ask-account-reset", label, a.c.AskAccountReset, Live(func() bool {
			d := a.c.State().AccountState
			return !ready() || a.c.State().AccountReset.Busy || d.Refreshing || d.UsageError != "" || d.Notice != ""
		})))
	}
	if action.Confirming && action.Attempt.Matches(d) {
		add(tr("Use a reset card?"))
		add(firstSettingsText(action.Attempt.Account.Email, tr("Current account")) + tr(" quota windows will reset immediately. This cannot be undone."))
		add(account.ResetExpiryText(action.Attempt.Credit))
		label := tr("Confirm")
		if action.Attempt.Submitted {
			label = tr("Retry fetching result")
		}
		if action.Busy {
			label = tr("Using card…")
		}
		nodes = append(nodes, settingsActions(settingsButton("cancel-account-reset", tr("Cancel"), a.c.CancelAccountReset, Live(func() bool { return a.c.State().AccountReset.Busy })), settingsButton("confirm-account-reset", label, a.c.ConfirmAccountReset, Live(func() bool { return !ready() || a.c.State().AccountReset.Busy || a.c.State().AccountState.Refreshing }))))
	}
	add(action.Notice)
	return settingsColumn(nodes...)
}
