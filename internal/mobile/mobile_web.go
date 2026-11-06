package mobile

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	phoneproxy "miya/internal/phone/proxy"
	"miya/internal/textutil"
	"miya/internal/webservice"
	"miya/internal/widgets"
)

func (m *App) openWebPort(port int) {
	m.openWebService(webservice.Service{Port: port})
}

func (m *App) openWebService(service webservice.Service) {
	if !m.Connected || m.Conn == nil || m.FilesCancel != nil || !m.State.WebPorts {
		return
	}
	proxy, err := phoneproxy.NewPhoneWebProxy(m.Conn, service.Port, service.Host, service.RuntimeToken)
	if err != nil {
		go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: tr("Unable to open web service"), Message: err.Error()})
		return
	}
	m.openFileBrowser(proxy, tr("Unable to open web service"))
}

func (m *App) webPage(c *ui.Context) {
	p := m.Pal
	state := m.ProjectWebServices()
	if state.ref.SessionID != "" {
		c.After(time.Second)
	}
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/web")
		ui.Text(c, map[bool]string{true: tr("Background tasks"), false: tr("Web services")}[m.WebDetails]).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		if m.homeAction(c, "refresh-web", tr("Refresh web services"), "refresh").Disabled(!m.Connected || !state.supported || state.Loading).Clicked() {
			m.clearWebServices()
		}
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(20).Gap(16).Children(func() {
			project := m.project(state.Project)
			ui.Text(c, textutil.FirstNonEmpty(project.Name, tr("Current project"))+map[bool]string{true: tr(" · This conversation"), false: tr(" · Project overview")}[state.ref.SessionID != ""]).FontSize(m.Font(22)).FontWeight(600)
			if m.WebDetails && state.Runtime != nil {
				m.runtimeDetails(c, state.Runtime.View)
			}
			m.webServicesList(c, state)
			if state.ref.SessionID != "" && state.runtimeSupported && !m.WebDetails {
				if m.pill(c, "runtime-details", tr("Background tasks"), false).Clicked() {
					m.WebDetails = true
				}
			}
			if m.WebDetails {
				return
			}
			if !m.WebManual && state.supported {
				if m.pill(c, "manual-port", tr("Enter port manually"), false).Clicked() {
					m.WebManual = true
				}
				return
			}
			ui.Text(c, tr("Enter the port of an HTTP service running on your computer, such as 3000, 5173, or 8080.")).FontSize(m.Font(15)).TextColor(p.muted).LineHeight(1.5)
			ui.TextInput(c.Key("port"), &m.WebPort).Label(tr("Web port")).Placeholder(tr("For example, 3000")).FillWidth().MinHeight(52).Padding(14).Background(p.surface).Radius(14).
				InputOptions(ui.InputOptions{Keyboard: ui.KeyboardNumber, Correction: ui.CorrectionOff, Return: ui.ReturnGo})
			port, err := webservice.ParsePort(strings.TrimSpace(m.WebPort))
			if m.WebPort != "" && err != nil {
				ui.Text(c, err.Error()).FontSize(m.Font(14)).TextColor(widgets.Danger)
			}
			if !m.Connected {
				ui.Text(c, tr("Connect to your desktop to open web services.")).FontSize(m.Font(14)).TextColor(p.muted)
			} else if !m.State.WebPorts {
				ui.Text(c, tr("Update Miya on your desktop first. If the background service is busy, it will update when idle.")).FontSize(m.Font(14)).TextColor(p.muted).LineHeight(1.5)
			}
			if m.pill(c, "open-web", tr("Open web service"), true).FillWidth().Disabled(err != nil || !m.Connected || !m.State.WebPorts || m.FilesCancel != nil).Clicked() {
				m.openWebPort(port)
			}
		})
	})
}
