package nsmobile

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/chat/model"
	phoneproxy "miya/internal/phone/proxy"
	phoneservices "miya/internal/phone/services"
	"miya/internal/remote"
	rt "miya/internal/runtime"
	runtimepresentation "miya/internal/runtime/presentation"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
)

type runtimeFetcher func(context.Context, *remote.Conn, string, model.ThreadRef) (tracker.ConversationRuntime, error)
type webProxyOpener func(*remote.Conn, webservice.Service) (string, func(), error)
type webServices struct {
	owner    composerOwner
	conn     *remote.Conn
	ref      model.ThreadRef
	project  string
	overview bool
	runtime  *rt.View
	cancel   context.CancelFunc
	loading  bool
	services []webservice.Service
	err      string
}

func (m *App) openWebServices() {
	var ref model.ThreadRef
	if m.Open != 0 {
		t := m.Viewer.Current()
		if t.ID == m.Open {
			ref = model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}
		}
	}
	m.cancelVoice()
	m.closeWebServices()
	m.web = &webServices{owner: m.composerOwner(), conn: m.Conn, project: m.ComposerProject(), ref: ref, overview: ref.SessionID == ""}
	m.pages.Open("web-services")
	m.refreshWebServices(m.web)
}
func (m *App) closeWebServices() {
	if s := m.web; s != nil {
		if s.cancel != nil {
			s.cancel()
		}
		m.web = nil
		if m.pages != nil && m.pages.Top() == "web-services" {
			m.pages.Open("")
		}
	}
}
func (m *App) webValid(s *webServices) bool {
	return m.web == s && s.owner == m.composerOwner() && s.conn == m.Conn && m.Connected && m.pages.Top() == "web-services"
}
func (m *App) refreshWebServices(s *webServices) {
	if !m.webValid(s) || s.loading {
		return
	}
	if s.overview {
		m.refreshProjectServices(s)
		return
	}
	if !m.State.Runtime || s.ref.SessionID == "" {
		s.err = tr("Update the desktop app to view runtime information for this conversation.")
		return
	}
	fetch := m.fetchRuntime
	if fetch == nil {
		if s.conn == nil {
			return
		}
		fetch = func(ctx context.Context, conn *remote.Conn, project string, ref model.ThreadRef) (tracker.ConversationRuntime, error) {
			return phoneservices.FetchPhoneRuntime(ctx, conn.DialFiles, project, ref)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	s.cancel, s.loading, s.err = cancel, true, ""
	go func() {
		result, err := fetch(ctx, s.conn, firstWebProject(s), s.ref)
		m.cx.Post(func() {
			if !m.webValid(s) {
				cancel()
				return
			}
			s.loading, s.err, s.services, s.runtime = false, "", nil, nil
			if err != nil || result.View.Key != tracker.KeyOf(s.ref) {
				s.err = tr("Unable to load web services. Refresh to try again.")
			} else {
				s.runtime = &result.View
				// Keep the adapter boundary strict even for a host-supplied fetcher.
				for _, service := range result.Services {
					if service.Host != "" && service.RuntimeToken != "" && service.Port > 0 && service.Port <= 65535 && webservice.ValidHost(service.Host, service.RuntimeToken) {
						s.services = append(s.services, service)
					}
				}
			}
			cancel()
			m.change()
		})
	}()
}
func firstWebProject(s *webServices) string {
	if s.project != "" {
		return s.project
	}
	return s.owner.project
}

func (m *App) refreshProjectServices(s *webServices) {
	project := firstWebProject(s)
	if !m.State.WebPorts || project == "" {
		return
	}
	fetch := m.fetchProjectServices
	if fetch == nil {
		if s.conn == nil {
			return
		}
		fetch = func(ctx context.Context, conn *remote.Conn, project string) ([]webservice.Service, error) {
			return phoneservices.FetchPhoneWebServices(ctx, conn.DialFiles, project)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	s.cancel, s.loading, s.err = cancel, true, ""
	go func() {
		defer cancel()
		services, err := fetch(ctx, s.conn, project)
		m.cx.Post(func() {
			if !m.webValid(s) {
				return
			}
			s.loading, s.services = false, nil
			if err != nil {
				s.err = tr("Unable to load web services. Refresh to try again.")
			} else {
				for _, service := range services {
					if service.Port > 0 && service.Port <= 65535 && webservice.ValidHost(service.Host, service.RuntimeToken) {
						s.services = append(s.services, service)
					}
				}
			}
			m.change()
		})
	}()
}

func (m *App) openWebService(s *webServices, service webservice.Service) {
	if !m.webValid(s) || !m.State.WebPorts || s.loading {
		return
	}
	// Listed endpoints must come from the current result. Verified entries
	// carry a runtime token which the desktop revalidates on access.
	found := false
	for _, candidate := range s.services {
		if candidate.Port == service.Port && candidate.Host == service.Host && candidate.RuntimeToken == service.RuntimeToken {
			found = true
			break
		}
	}
	if !found {
		return
	}
	m.openWebServiceProxy(s, service)
}

func (m *App) openWebServiceProxy(s *webServices, service webservice.Service) {
	if !m.webValid(s) || !m.State.WebPorts {
		return
	}
	open := m.openWebProxy
	if open == nil {
		open = func(conn *remote.Conn, service webservice.Service) (string, func(), error) {
			p, err := phoneproxy.NewPhoneWebProxy(conn, service.Port, service.Host, service.RuntimeToken)
			if err != nil {
				return "", nil, err
			}
			return p.URL, func() { p.Close() }, nil
		}
	}
	raw, close, err := open(s.conn, service)
	if err != nil {
		s.err = tr("Unable to open web service") + ": " + err.Error()
		return
	}
	m.closeFileBrowser()
	m.closeFiles, m.webBrowser = close, true
	m.filesURL.Set(raw)
	m.closeWebServices()
	m.pages.Open("")
}
func (m *App) webServicesSheet(cx *core.Scope) core.Node {
	s := m.web
	if s == nil {
		return m.groupedPage(cx, groupedState(cx, "globe", func() string {
			return tr("Connect to your desktop to view project web services.")
		}, nil, nil))
	}
	cx.Effect(func() {
		m.revision.Get()
		if !m.webValid(s) {
			cx.Post(m.act(func() {
				if m.web == s {
					m.closeWebServices()
					m.pages.Open("")
				}
			}))
		}
	})
	var poll func()
	poll = func() {
		if m.webValid(s) {
			m.refreshWebServices(s)
			m.change()
			cx.AfterFunc(5*time.Second, poll)
		}
	}
	stop := cx.AfterFunc(5*time.Second, poll)
	cx.Cleanup(func() {
		stop()
		if m.web == s {
			m.closeWebServices()
		}
	})
	manual, details := core.NewSignal(cx, false), core.NewSignal(cx, false)
	portText := core.NewSignal(cx, "")
	status := live(m, func() string {
		switch {
		case s.err != "":
			return s.err
		case s.loading && len(s.services) == 0:
			return tr("Finding project web services…")
		case len(s.services) == 0:
			if s.overview {
				return tr("No project HTTP services found. These candidates may belong to other conversations.")
			}
			return tr("No verified HTTP service for this conversation.")
		}
		return ""
	})
	return m.groupedPage(cx,
		core.Dynamic(func(cx *core.Scope) core.Node {
			m.revision.Get()
			title := tr("This conversation")
			if s.overview {
				title = tr("Project overview")
			}
			rows := []gRow{{ID: "web-refresh", Label: tr("Refresh"), Icon: "refresh", Action: true, Disabled: live(m, func() bool { return s.loading }), Press: func() { m.refreshWebServices(s) }}}
			if s.ref.SessionID != "" {
				label := tr("Project overview")
				if s.overview {
					label = tr("This conversation")
				}
				rows = append(rows, gRow{ID: "web-scope", Label: label, Icon: "folder", Action: true, Disabled: live(m, func() bool { return s.loading }), Press: func() {
					s.overview = !s.overview
					s.err, s.services, s.runtime = "", nil, nil
					details.Set(false)
					m.refreshWebServices(s)
				}})
			}
			if s.runtime != nil {
				rows = append(rows, gRow{ID: "web-runtime", Label: tr("Background tasks"), Icon: "terminal-2", Action: true, Press: func() { details.Update(func(show bool) bool { return !show }) }})
			}
			if m.State.WebPorts {
				rows = append(rows, gRow{ID: "web-manual", Label: tr("Enter port manually"), Icon: "globe", Action: true, Press: func() { manual.Update(func(show bool) bool { return !show }) }})
			}
			return m.group(cx, title, nil, rows...)
		}),
		core.Show{When: manual.Get, Then: func(cx *core.Scope) core.Node {
			open := func() {
				port, err := webservice.ParsePort(portText.Get())
				if err == nil {
					cx.DismissKeyboard()
					m.act(func() { m.openWebServiceProxy(s, webservice.Service{Port: port}) })()
				}
			}
			return groupedSection(cx, tr("Web port"), nil,
				ui.Input{TestID: "web-manual-port", Label: tr("Web port"), Placeholder: tr("For example, 3000"), Value: portText.Get, OnChange: portText.Set, OnSubmit: func(string) { open() }},
				m.groupedRow(cx, gRow{ID: "web-manual-open", Label: tr("Open web service"), Icon: "globe", Action: true, Press: open,
					Disabled: live(m, func() bool { _, err := webservice.ParsePort(portText.Get()); return err != nil || !m.State.WebPorts })}))
		}},
		core.Dynamic(func(cx *core.Scope) core.Node {
			m.revision.Get()
			if details.Get() && s.runtime != nil {
				return m.runtimeDetails(cx, *s.runtime)
			}
			return nil
		}),
		groupedState(cx, "globe", status, live(m, func() bool { return s.loading }),
			live(m, func() bool { return s.err != "" })),
		core.Dynamic(func(cx *core.Scope) core.Node {
			m.revision.Get()
			if len(s.services) == 0 {
				return core.Group(nil)
			}
			var rows []gRow
			for _, service := range s.services {
				address := "http://" + net.JoinHostPort(service.Host, strconv.Itoa(service.Port))
				rows = append(rows, gRow{ID: "web-service-" + strconv.Itoa(service.Port), Label: address,
					Icon: "globe", Chevron: true, Disabled: live(m, func() bool { return s.loading || !m.State.WebPorts }),
					Press: func() { m.openWebService(s, service) }})
			}
			var footer core.Node
			if s.overview {
				footer = groupedFooter(cx, "", func() string { return tr("Project candidate · Conversation ownership unconfirmed") })
			}
			return m.group(cx, "", footer, rows...)
		}),
	)
}

func (m *App) runtimeDetails(cx *core.Scope, view rt.View) core.Node {
	var tasks, processes []gRow
	for _, task := range view.Tasks {
		label := task.Description
		if label == "" {
			label = tr("Background tasks")
		}
		tasks = append(tasks, gRow{Label: label, Detail: runtimepresentation.RuntimeStatus(task.Status) + " · " + runtimepresentation.RuntimeKind(task.Kind)})
	}
	if len(tasks) == 0 {
		label := tr("Native task list unconfirmed")
		if view.TasksKnown {
			label = tr("No native background tasks")
		}
		tasks = append(tasks, gRow{Label: label})
	}
	for _, process := range view.Processes {
		processes = append(processes, gRow{Label: fmt.Sprintf("%s · PID %d", process.Name, process.Identity.PID), Detail: runtimepresentation.RuntimeStatus(process.Status)})
	}
	if len(processes) == 0 {
		processes = append(processes, gRow{Label: tr("No process identity available for verification")})
	}
	for _, endpoint := range view.Endpoints {
		if endpoint.Protocol != "http" {
			processes = append(processes, gRow{Label: fmt.Sprintf("%s:%d · TCP", endpoint.Host, endpoint.Port)})
		}
	}
	var footer core.Node
	if view.ObservedAt > 0 {
		footer = groupedFooter(cx, "", func() string {
			return tr("Last verified ") + time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")
		})
	}
	return core.Group([]core.Node{m.group(cx, tr("Background tasks"), nil, tasks...), m.group(cx, tr("System process"), footer, processes...)})
}

func (m *App) openLocalWebLink(port int, path string) {
	if !m.Connected || m.Conn == nil {
		m.SendErr = tr("Connect to your desktop to open web services.")
		return
	}
	if !m.State.WebPorts {
		m.SendErr = tr("Update Miya on your desktop first. If the background service is busy, it will update when idle.")
		return
	}
	proxy, err := phoneproxy.NewPhoneWebProxy(m.Conn, port, "")
	if err != nil {
		m.SendErr = tr("Unable to open web service") + ": " + err.Error()
		return
	}
	m.closeWebServices()
	m.closeFileBrowser()
	m.closeFiles, m.webBrowser = proxy.Close, true
	m.filesURL.Set(proxy.URL + "?path=" + url.QueryEscape(path))
	m.pages.Open("")
}
