package mobile

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

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
	"miya/internal/textutil"
	"miya/internal/webservice"
	"miya/internal/widgets"
)

type mobileWebServices struct {
	ref              model.ThreadRef
	runtimeSupported bool
	Runtime          *tracker.ConversationRuntime
	Project          string
	conn             *remote.Conn
	supported        bool
	Loading          bool
	requestedAt      time.Time
	Services         []webservice.Service
	Err              string
	Cancel           context.CancelFunc
}

func (m *App) clearWebServices() {
	if m.WebServices != nil && m.WebServices.Cancel != nil {
		m.WebServices.Cancel()
	}
	m.WebServices = nil
}

func (m *App) OpenWebServices(project string) {
	m.clearWebServices()
	m.WebProject, m.WebManual, m.WebDetails = project, false, false
	m.WebRef = model.ThreadRef{}
	if m.Open != 0 {
		t, p := m.find(m.Open)
		if p.Path == project && t.ServerID != "" {
			m.WebRef = model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}
		}
	}
	m.Router().Push("/web")
}

func (m *App) ProjectWebServices() *mobileWebServices {
	project := m.WebProject
	if project == "" {
		project = m.selectedProject().Path
	}
	if state := m.WebServices; state != nil && state.Project == project && state.conn == m.Conn && state.ref == m.WebRef && state.runtimeSupported == m.State.Runtime && state.supported == m.webServicesSupported() {
		if state.ref.SessionID != "" && !state.Loading && time.Since(state.requestedAt) > 5*time.Second {
			m.requestWebServices(state)
		}
		return state
	}
	m.clearWebServices()
	state := &mobileWebServices{Project: project, conn: m.Conn, ref: m.WebRef, runtimeSupported: m.State.Runtime, supported: m.webServicesSupported()}
	m.WebServices = state
	m.requestWebServices(state)
	return state
}

func (m *App) requestWebServices(state *mobileWebServices) {
	if !m.Connected || m.Conn == nil || !state.supported || state.Project == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	state.Cancel, state.Loading, state.requestedAt = cancel, true, time.Now()
	gen := m.Generation
	go func() {
		defer cancel()
		var services []webservice.Service
		var result *tracker.ConversationRuntime
		var err error
		if state.ref.SessionID != "" {
			var value tracker.ConversationRuntime
			value, err = FetchPhoneRuntime(ctx, state.conn.DialFiles, state.Project, state.ref)
			services = value.Services
			result = &value
		} else {
			services, err = FetchPhoneWebServices(ctx, state.conn.DialFiles, state.Project)
		}
		m.onMain(gen, func() {
			if m.WebServices != state || m.Conn != state.conn {
				return
			}
			state.Loading = false
			state.Err = ""
			if err == nil {
				state.Services, state.Runtime = services, result
			}
			if err != nil {
				state.Err = tr("Unable to load runtime information. Refresh or enter a port manually.")
				if state.Runtime != nil {
					widgets.RuntimeObservationLost(state.Runtime)
				}
				state.Services = nil
			}
		})
	}()
}

func FetchPhoneWebServices(ctx context.Context, dial func(context.Context) (net.Conn, error), project string) ([]webservice.Service, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://desktop/"+phoneproto.ProjectFileID(project)+"/"+webservice.ProjectFile, nil)
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

func (m *App) webServicesList(c *ui.Context, state *mobileWebServices) {
	p := m.Pal
	if !m.Connected {
		ui.Text(c, tr("Connect to your desktop to view project web services.")).FontSize(m.Font(14)).TextColor(p.muted)
		return
	}
	if !state.supported {
		if m.State.WebPorts {
			ui.Text(c, tr("Update the desktop app to view runtime information for this conversation.")).FontSize(m.Font(14)).TextColor(p.muted).LineHeight(1.5)
		}
		return
	}
	if state.ref.SessionID != "" && state.Runtime != nil && state.Runtime.View.Notice != "" {
		ui.Text(c, state.Runtime.View.Notice).FontSize(m.Font(13)).TextColor(p.muted).LineHeight(1.5)
	}
	if state.Loading && len(state.Services) == 0 && state.Runtime == nil {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, tr("Finding project web services…")).FontSize(m.Font(15)).TextColor(p.muted)
		})
		return
	}
	if state.Err != "" {
		ui.Text(c, state.Err).FontSize(m.Font(14)).TextColor(p.muted).LineHeight(1.5)
		return
	}
	if len(state.Services) == 0 {
		ui.Text(c, map[bool]string{true: tr("No verified HTTP service for this conversation. Refresh or enter a port manually."), false: tr("No project HTTP services found. These candidates may belong to other conversations.")}[state.ref.SessionID != ""]).FontSize(m.Font(15)).TextColor(p.muted).LineHeight(1.5)
		return
	}
	for _, service := range state.Services {
		label := "http://" + net.JoinHostPort(textutil.FirstNonEmpty(service.Host, "localhost"), strconv.Itoa(service.Port))
		if m.phoneButton(c.Key(service.Port)).FillWidth().MinHeight(64).Padding(14).Gap(12).Radius(14).Background(p.surface).
			Label(tr("Open ") + label).Disabled(m.FilesCancel != nil).Children(func() {
			widgets.Icon(c, "globe", 22, p.ink)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, tr("Port ")+strconv.Itoa(service.Port)).FontSize(m.Font(17)).FontWeight(600)
				ui.Text(c, textutil.FirstNonEmpty(service.Process, tr("Web services"))+" · HTTP").FontSize(m.Font(13)).TextColor(p.muted).SingleLine().Ellipsis("…")
				if state.ref.SessionID == "" {
					ui.Text(c, m.runtimeServiceOwner(service)).FontSize(m.Font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
				}
			})
			widgets.Icon(c, "chevron-right", 16, p.muted)
		}).Clicked() {
			m.openWebService(service)
		}
	}
}

func (m *App) webServicesSupported() bool {
	if m.WebRef.SessionID != "" {
		return m.State.Runtime
	}
	return m.State.WebServices
}

func FetchPhoneRuntime(ctx context.Context, dial func(context.Context) (net.Conn, error), project string, ref model.ThreadRef) (tracker.ConversationRuntime, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://desktop/"+phoneproto.ProjectFileID(project)+"/.miya-runtime?ref="+url.QueryEscape(ref.URL()), nil)
	if err != nil {
		return tracker.ConversationRuntime{}, err
	}
	response, err := client.Do(req)
	if err != nil {
		return tracker.ConversationRuntime{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return tracker.ConversationRuntime{}, errors.New(tr("Conversation runtime information unavailable"))
	}
	var result tracker.ConversationRuntime
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return result, err
	}
	if result.View.Key != tracker.KeyOf(ref) {
		return tracker.ConversationRuntime{}, errors.New(tr("Conversation runtime identity mismatch"))
	}
	for _, service := range result.Services {
		if service.Host == "" || !webservice.ValidHost(service.Host, service.RuntimeToken) || service.Port < 1 || service.Port > 65535 || service.RuntimeToken == "" {
			return tracker.ConversationRuntime{}, errors.New(tr("Invalid runtime service endpoint"))
		}
	}
	return result, nil
}

func (m *App) runtimeServiceOwner(service webservice.Service) string {
	if service.Owner == nil {
		return tr("Project candidate · Conversation ownership unconfirmed")
	}
	for _, project := range m.State.Projects {
		for _, thread := range project.Threads {
			if thread.ServerID == service.Owner.SessionID && model.ThreadAgent(thread) == service.Owner.Backend {
				return tr("From conversation: ") + thread.Title
			}
		}
	}
	return tr("Verified conversation service")
}

func (m *App) runtimeDetails(c *ui.Context, view rt.View) {
	p := m.Pal
	ui.Text(c, tr("Background tasks")).FontSize(m.Font(15)).FontWeight(600)
	if len(view.Tasks) == 0 {
		ui.Text(c, map[bool]string{true: tr("No native background tasks"), false: tr("Native task list unconfirmed")}[view.TasksKnown]).FontSize(m.Font(14)).TextColor(p.muted)
	}
	for _, task := range view.Tasks {
		ui.Column(c.Key("task-" + task.ID)).FillWidth().Padding(14).Gap(6).Radius(14).Background(p.surface).Children(func() {
			ui.Text(c, textutil.FirstNonEmpty(task.Description, tr("Background tasks"))).FontSize(m.Font(15)).LineHeight(1.4)
			ui.Text(c, widgets.RuntimeStatus(task.Status)+" · "+widgets.RuntimeKind(task.Kind)).FontSize(m.Font(13)).TextColor(p.muted)
		})
	}
	ui.Text(c, tr("System process")).FontSize(m.Font(15)).FontWeight(600)
	if len(view.Processes) == 0 {
		ui.Text(c, tr("No process identity available for verification")).FontSize(m.Font(14)).TextColor(p.muted)
	}
	for _, process := range view.Processes {
		ui.Text(c, fmt.Sprintf("%s · PID %d · %s", process.Name, process.Identity.PID, widgets.RuntimeStatus(process.Status))).FontSize(m.Font(14)).TextColor(p.muted).LineHeight(1.5)
	}
	for _, endpoint := range view.Endpoints {
		if endpoint.Protocol != "http" {
			ui.Text(c, fmt.Sprintf("%s:%d · TCP", endpoint.Host, endpoint.Port)).FontSize(m.Font(13)).TextColor(p.muted)
		}
	}
	if view.ObservedAt > 0 {
		ui.Text(c, tr("Last verified ")+time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")).FontSize(m.Font(12)).TextColor(p.muted)
	}
}
