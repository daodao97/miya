package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/webservice"
)

func newPhoneWebProxy(conn *remote.Conn, port int, host string, tokens ...string) (*phoneFileProxy, error) {
	value := strconv.Itoa(port)
	if _, err := webservice.ParsePort(value); err != nil {
		return nil, err
	}
	token := ""
	if len(tokens) > 0 {
		token = tokens[0]
	}
	if !webservice.ValidHost(host, token) {
		return nil, errors.New(tr("Invalid local service address"))
	}
	p := new(phoneFileProxy)
	p.webHost = host
	if len(tokens) > 0 {
		p.webToken = tokens[0]
	}
	p.conn.Store(conn)
	err := p.startProxy(webservice.Prefix+value, "http://localhost:"+value, func(ctx context.Context) (net.Conn, error) {
		current := p.conn.Load()
		if current == nil {
			return nil, errors.New(tr("Desktop connection lost"))
		}
		return current.DialFiles(ctx)
	})
	return p, err
}

func (m *mobileApp) openWebPort(port int) {
	m.openWebService(webservice.Service{Port: port})
}

func (m *mobileApp) openWebService(service webservice.Service) {
	if !m.connected || m.conn == nil || m.filesCancel != nil || !m.state.WebPorts {
		return
	}
	proxy, err := newPhoneWebProxy(m.conn, service.Port, service.Host, service.RuntimeToken)
	if err != nil {
		go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: tr("Unable to open web service"), Message: err.Error()})
		return
	}
	m.openFileBrowser(proxy, tr("Unable to open web service"))
}

func (m *mobileApp) webPage(c *ui.Context) {
	p := m.pal
	state := m.projectWebServices()
	if state.ref.SessionID != "" {
		c.After(time.Second)
	}
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/web")
		ui.Text(c, map[bool]string{true: tr("Background tasks"), false: tr("Web services")}[m.webDetails]).Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		if m.homeAction(c, "refresh-web", tr("Refresh web services"), "refresh").Disabled(!m.connected || !state.supported || state.loading).Clicked() {
			m.clearWebServices()
		}
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(20).Gap(16).Children(func() {
			project := m.project(state.project)
			ui.Text(c, textutil.FirstNonEmpty(project.Name, tr("Current project"))+map[bool]string{true: tr(" · This conversation"), false: tr(" · Project overview")}[state.ref.SessionID != ""]).FontSize(m.font(22)).FontWeight(600)
			if m.webDetails && state.runtime != nil {
				m.runtimeDetails(c, state.runtime.View)
			}
			m.webServicesList(c, state)
			if state.ref.SessionID != "" && state.runtimeSupported && !m.webDetails {
				if m.pill(c, "runtime-details", tr("Background tasks"), false).Clicked() {
					m.webDetails = true
				}
			}
			if m.webDetails {
				return
			}
			if !m.webManual && state.supported {
				if m.pill(c, "manual-port", tr("Enter port manually"), false).Clicked() {
					m.webManual = true
				}
				return
			}
			ui.Text(c, tr("Enter the port of an HTTP service running on your computer, such as 3000, 5173, or 8080.")).FontSize(m.font(15)).TextColor(p.muted).LineHeight(1.5)
			ui.TextInput(c.Key("port"), &m.webPort).Label(tr("Web port")).Placeholder(tr("For example, 3000")).FillWidth().MinHeight(52).Padding(14).Background(p.surface).Radius(14).
				InputOptions(ui.InputOptions{Keyboard: ui.KeyboardNumber, Correction: ui.CorrectionOff, Return: ui.ReturnGo})
			port, err := webservice.ParsePort(strings.TrimSpace(m.webPort))
			if m.webPort != "" && err != nil {
				ui.Text(c, err.Error()).FontSize(m.font(14)).TextColor(danger)
			}
			if !m.connected {
				ui.Text(c, tr("Connect to your desktop to open web services.")).FontSize(m.font(14)).TextColor(p.muted)
			} else if !m.state.WebPorts {
				ui.Text(c, tr("Update Miya on your desktop first. If the background service is busy, it will update when idle.")).FontSize(m.font(14)).TextColor(p.muted).LineHeight(1.5)
			}
			if m.pill(c, "open-web", tr("Open web service"), true).FillWidth().Disabled(err != nil || !m.connected || !m.state.WebPorts || m.filesCancel != nil).Clicked() {
				m.openWebPort(port)
			}
		})
	})
}
