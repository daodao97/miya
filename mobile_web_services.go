package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"miya/internal/model"
	"miya/internal/remote"
	rt "miya/internal/runtime"
	"miya/internal/textutil"
	"miya/internal/webservice"

	"github.com/egoist/mygo/ui"
)

type mobileWebServices struct {
	ref              ThreadRef
	runtimeSupported bool
	runtime          *conversationRuntime
	project          string
	conn             *remote.Conn
	supported        bool
	loading          bool
	requestedAt      time.Time
	services         []webservice.Service
	err              string
	cancel           context.CancelFunc
}

func (m *mobileApp) clearWebServices() {
	if m.webServices != nil && m.webServices.cancel != nil {
		m.webServices.cancel()
	}
	m.webServices = nil
}

func (m *mobileApp) openWebServices(project string) {
	m.clearWebServices()
	m.webProject, m.webManual, m.webDetails = project, false, false
	m.webRef = ThreadRef{}
	if m.open != 0 {
		t, p := m.find(m.open)
		if p.Path == project && t.ServerID != "" {
			m.webRef = ThreadRef{Source: m.state.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}
		}
	}
	m.router().Push("/web")
}

func (m *mobileApp) projectWebServices() *mobileWebServices {
	project := m.webProject
	if project == "" {
		project = m.selectedProject().Path
	}
	if state := m.webServices; state != nil && state.project == project && state.conn == m.conn && state.ref == m.webRef && state.runtimeSupported == m.state.Runtime && state.supported == m.webServicesSupported() {
		if state.ref.SessionID != "" && !state.loading && time.Since(state.requestedAt) > 5*time.Second {
			m.requestWebServices(state)
		}
		return state
	}
	m.clearWebServices()
	state := &mobileWebServices{project: project, conn: m.conn, ref: m.webRef, runtimeSupported: m.state.Runtime, supported: m.webServicesSupported()}
	m.webServices = state
	m.requestWebServices(state)
	return state
}

func (m *mobileApp) requestWebServices(state *mobileWebServices) {
	if !m.connected || m.conn == nil || !state.supported || state.project == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	state.cancel, state.loading, state.requestedAt = cancel, true, time.Now()
	gen := m.generation
	go func() {
		defer cancel()
		var services []webservice.Service
		var result *conversationRuntime
		var err error
		if state.ref.SessionID != "" {
			var value conversationRuntime
			value, err = fetchPhoneRuntime(ctx, state.conn.DialFiles, state.project, state.ref)
			services = value.Services
			result = &value
		} else {
			services, err = fetchPhoneWebServices(ctx, state.conn.DialFiles, state.project)
		}
		m.onMain(gen, func() {
			if m.webServices != state || m.conn != state.conn {
				return
			}
			state.loading = false
			state.err = ""
			if err == nil {
				state.services, state.runtime = services, result
			}
			if err != nil {
				state.err = tr("Unable to load runtime information. Refresh or enter a port manually.")
				if state.runtime != nil {
					runtimeObservationLost(state.runtime)
				}
				state.services = nil
			}
		})
	}()
}

func fetchPhoneWebServices(ctx context.Context, dial func(context.Context) (net.Conn, error), project string) ([]webservice.Service, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://desktop/"+projectFileID(project)+"/"+webservice.ProjectFile, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("project web services unavailable")
	}
	var services []webservice.Service
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&services); err != nil {
		return nil, err
	}
	for _, service := range services {
		if !webservice.ValidHost(service.Host, service.RuntimeToken) {
			return nil, errors.New("invalid service address")
		}
		if _, err := webservice.ParsePort(strconv.Itoa(service.Port)); err != nil {
			return nil, err
		}
	}
	return services, nil
}

func (m *mobileApp) webServicesList(c *ui.Context, state *mobileWebServices) {
	p := m.pal
	if !m.connected {
		ui.Text(c, tr("Connect to your desktop to view project web services.")).FontSize(m.font(14)).TextColor(p.muted)
		return
	}
	if !state.supported {
		if m.state.WebPorts {
			ui.Text(c, tr("Update the desktop app to view runtime information for this conversation.")).FontSize(m.font(14)).TextColor(p.muted).LineHeight(1.5)
		}
		return
	}
	if state.ref.SessionID != "" && state.runtime != nil && state.runtime.View.Notice != "" {
		ui.Text(c, state.runtime.View.Notice).FontSize(m.font(13)).TextColor(p.muted).LineHeight(1.5)
	}
	if state.loading && len(state.services) == 0 && state.runtime == nil {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, tr("Finding project web services…")).FontSize(m.font(15)).TextColor(p.muted)
		})
		return
	}
	if state.err != "" {
		ui.Text(c, state.err).FontSize(m.font(14)).TextColor(p.muted).LineHeight(1.5)
		return
	}
	if len(state.services) == 0 {
		ui.Text(c, map[bool]string{true: tr("No verified HTTP service for this conversation. Refresh or enter a port manually."), false: tr("No project HTTP services found. These candidates may belong to other conversations.")}[state.ref.SessionID != ""]).FontSize(m.font(15)).TextColor(p.muted).LineHeight(1.5)
		return
	}
	for _, service := range state.services {
		label := "http://" + net.JoinHostPort(textutil.FirstNonEmpty(service.Host, "localhost"), strconv.Itoa(service.Port))
		if m.phoneButton(c.Key(service.Port)).FillWidth().MinHeight(64).Padding(14).Gap(12).Radius(14).Background(p.surface).
			Label(tr("Open ") + label).Disabled(m.filesCancel != nil).Children(func() {
			icon(c, "globe", 22, p.ink)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, tr("Port ")+strconv.Itoa(service.Port)).FontSize(m.font(17)).FontWeight(600)
				ui.Text(c, textutil.FirstNonEmpty(service.Process, tr("Web services"))+" · HTTP").FontSize(m.font(13)).TextColor(p.muted).SingleLine().Ellipsis("…")
				if state.ref.SessionID == "" {
					ui.Text(c, m.runtimeServiceOwner(service)).FontSize(m.font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
				}
			})
			icon(c, "chevron-right", 16, p.muted)
		}).Clicked() {
			m.openWebService(service)
		}
	}
}

func (m *mobileApp) webServicesSupported() bool {
	if m.webRef.SessionID != "" {
		return m.state.Runtime
	}
	return m.state.WebServices
}
func fetchPhoneRuntime(ctx context.Context, dial func(context.Context) (net.Conn, error), project string, ref ThreadRef) (conversationRuntime, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://desktop/"+projectFileID(project)+"/.miya-runtime?ref="+url.QueryEscape(ref.URL()), nil)
	if err != nil {
		return conversationRuntime{}, err
	}
	response, err := client.Do(req)
	if err != nil {
		return conversationRuntime{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return conversationRuntime{}, errors.New(tr("Conversation runtime information unavailable"))
	}
	var result conversationRuntime
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return result, err
	}
	if result.View.Key != runtimeKey(ref) {
		return conversationRuntime{}, errors.New(tr("Conversation runtime identity mismatch"))
	}
	for _, service := range result.Services {
		if service.Host == "" || !webservice.ValidHost(service.Host, service.RuntimeToken) || service.Port < 1 || service.Port > 65535 || service.RuntimeToken == "" {
			return conversationRuntime{}, errors.New(tr("Invalid runtime service endpoint"))
		}
	}
	return result, nil
}

func (m *mobileApp) runtimeServiceOwner(service webservice.Service) string {
	if service.Owner == nil {
		return tr("Project candidate · Conversation ownership unconfirmed")
	}
	for _, project := range m.state.Projects {
		for _, thread := range project.Threads {
			if thread.ServerID == service.Owner.SessionID && model.ThreadAgent(thread) == service.Owner.Backend {
				return tr("From conversation: ") + thread.Title
			}
		}
	}
	return tr("Verified conversation service")
}

func (m *mobileApp) runtimeDetails(c *ui.Context, view rt.View) {
	p := m.pal
	ui.Text(c, tr("Background tasks")).FontSize(m.font(15)).FontWeight(600)
	if len(view.Tasks) == 0 {
		ui.Text(c, map[bool]string{true: tr("No native background tasks"), false: tr("Native task list unconfirmed")}[view.TasksKnown]).FontSize(m.font(14)).TextColor(p.muted)
	}
	for _, task := range view.Tasks {
		ui.Column(c.Key("task-" + task.ID)).FillWidth().Padding(14).Gap(6).Radius(14).Background(p.surface).Children(func() {
			ui.Text(c, textutil.FirstNonEmpty(task.Description, tr("Background tasks"))).FontSize(m.font(15)).LineHeight(1.4)
			ui.Text(c, runtimeStatus(task.Status)+" · "+runtimeKind(task.Kind)).FontSize(m.font(13)).TextColor(p.muted)
		})
	}
	ui.Text(c, tr("System process")).FontSize(m.font(15)).FontWeight(600)
	if len(view.Processes) == 0 {
		ui.Text(c, tr("No process identity available for verification")).FontSize(m.font(14)).TextColor(p.muted)
	}
	for _, process := range view.Processes {
		ui.Text(c, fmt.Sprintf("%s · PID %d · %s", process.Name, process.Identity.PID, runtimeStatus(process.Status))).FontSize(m.font(14)).TextColor(p.muted).LineHeight(1.5)
	}
	for _, endpoint := range view.Endpoints {
		if endpoint.Protocol != "http" {
			ui.Text(c, fmt.Sprintf("%s:%d · TCP", endpoint.Host, endpoint.Port)).FontSize(m.font(13)).TextColor(p.muted)
		}
	}
	if view.ObservedAt > 0 {
		ui.Text(c, tr("Last verified ")+time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")).FontSize(m.font(12)).TextColor(p.muted)
	}
}
