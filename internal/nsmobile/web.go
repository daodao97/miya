package nsmobile

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"miya/internal/chat/model"
	phoneproxy "miya/internal/phone/proxy"
	phoneservices "miya/internal/phone/services"
	"miya/internal/remote"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
)

type runtimeFetcher func(context.Context, *remote.Conn, string, model.ThreadRef) (tracker.ConversationRuntime, error)
type webProxyOpener func(*remote.Conn, webservice.Service) (string, func(), error)
type webServices struct {
	owner    composerOwner
	conn     *remote.Conn
	ref      model.ThreadRef
	cancel   context.CancelFunc
	loading  bool
	services []webservice.Service
	err      string
}

func (m *App) openWebServices() {
	m.cancelVoice()
	m.closeWebServices()
	t := m.Viewer.Current()
	m.web = &webServices{owner: m.composerOwner(), conn: m.Conn, ref: model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}}
	m.sheet.Set("web-services")
	m.refreshWebServices(m.web)
}
func (m *App) closeWebServices() {
	if s := m.web; s != nil {
		if s.cancel != nil {
			s.cancel()
		}
		m.web = nil
		if m.sheet != nil && m.sheet.Get() == "web-services" {
			m.sheet.Set("")
		}
	}
}
func (m *App) webValid(s *webServices) bool {
	return m.web == s && s.owner == m.composerOwner() && s.conn == m.Conn && m.Connected && m.sheet.Get() == "web-services"
}
func (m *App) refreshWebServices(s *webServices) {
	if !m.webValid(s) || s.loading {
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
		result, err := fetch(ctx, s.conn, s.owner.project, s.ref)
		m.cx.Post(func() {
			if !m.webValid(s) {
				cancel()
				return
			}
			s.loading, s.err, s.services = false, "", nil
			if err != nil || result.View.Key != tracker.KeyOf(s.ref) {
				s.err = tr("Unable to load web services. Refresh to try again.")
			} else {
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
func (m *App) openWebService(s *webServices, service webservice.Service) {
	if !m.webValid(s) || !m.State.WebPorts || s.loading {
		return
	}
	// Only entries in this current verified runtime result can open. The proxy
	// passes RuntimeToken to the desktop, which validates it again on access.
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
	m.sheet.Set("")
}
func (m *App) webServicesSheet(cx *core.Scope) core.Node {
	s := m.web
	if s == nil {
		return text(tr("Connect to your desktop to view project web services."))
	}
	cx.Effect(func() {
		m.revision.Get()
		if !m.webValid(s) {
			cx.Post(m.act(func() {
				if m.web == s {
					m.closeWebServices()
					m.sheet.Set("")
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
	return scroll(text(tr("Web services")), text(tr(" · This conversation")),
		m.button("refresh-web", tr("Refresh web services"), live(m, func() bool { return s.loading || !m.Connected }), func() { m.refreshWebServices(s) }),
		core.Text{TestID: "web-status", ContentFunc: live(m, func() string {
			if s.loading {
				return tr("Finding project web services…")
			}
			if s.err != "" {
				return s.err
			}
			if len(s.services) == 0 {
				return tr("No verified HTTP service for this conversation.")
			}
			return ""
		})},
		core.Each(live(m, func() []webservice.Service { return s.services }), func(v webservice.Service) string { return v.Host + ":" + strconv.Itoa(v.Port) }, func(cx *core.Scope, service func() webservice.Service, _ func() int) core.Node {
			v := service()
			return m.button("web-service-"+strconv.Itoa(v.Port), tr("Open ")+"http://"+net.JoinHostPort(v.Host, strconv.Itoa(v.Port)), live(m, func() bool { return s.loading || !m.State.WebPorts }), func() { m.openWebService(s, service()) })
		}),
		m.button("web-done", tr("Done"), nil, func() { m.closeWebServices(); m.sheet.Set("") }))
}
