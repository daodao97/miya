package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
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
	services         []phoneWebService
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
			m.webRef = ThreadRef{Source: m.state.ReferenceSource, Backend: threadAgent(t), SessionID: t.ServerID}
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
		var services []phoneWebService
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
				state.err = "无法读取运行信息，请刷新或手动输入端口。"
				if state.runtime != nil {
					runtimeObservationLost(state.runtime)
				}
				state.services = nil
			}
		})
	}()
}

func fetchPhoneWebServices(ctx context.Context, dial func(context.Context) (net.Conn, error), project string) ([]phoneWebService, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://desktop/"+projectFileID(project)+"/"+projectWebServicesPath, nil)
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
	var services []phoneWebService
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&services); err != nil {
		return nil, err
	}
	for _, service := range services {
		if !validWebServiceHost(service.Host, service.RuntimeToken) {
			return nil, errors.New("invalid service address")
		}
		if _, err := parseWebPort(strconv.Itoa(service.Port)); err != nil {
			return nil, err
		}
	}
	return services, nil
}

func (m *mobileApp) webServicesList(c *ui.Context, state *mobileWebServices) {
	p := m.pal
	if !m.connected {
		ui.Text(c, "连接桌面端后可查看项目 Web 服务。").FontSize(m.font(14)).TextColor(p.muted)
		return
	}
	if !state.supported {
		if m.state.WebPorts {
			ui.Text(c, "更新桌面端后可查看本对话运行信息。").FontSize(m.font(14)).TextColor(p.muted).LineHeight(1.5)
		}
		return
	}
	if state.ref.SessionID != "" && state.runtime != nil && state.runtime.View.Notice != "" {
		ui.Text(c, state.runtime.View.Notice).FontSize(m.font(13)).TextColor(p.muted).LineHeight(1.5)
	}
	if state.loading && len(state.services) == 0 && state.runtime == nil {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "正在查找项目 Web 服务…").FontSize(m.font(15)).TextColor(p.muted)
		})
		return
	}
	if state.err != "" {
		ui.Text(c, state.err).FontSize(m.font(14)).TextColor(p.muted).LineHeight(1.5)
		return
	}
	if len(state.services) == 0 {
		ui.Text(c, map[bool]string{true: "尚未核验到本对话的 HTTP 服务，可刷新或手动输入端口。", false: "未发现项目 HTTP 服务，这些候选不代表由本对话创建。"}[state.ref.SessionID != ""]).FontSize(m.font(15)).TextColor(p.muted).LineHeight(1.5)
		return
	}
	for _, service := range state.services {
		label := "http://" + net.JoinHostPort(firstNonEmpty(service.Host, "localhost"), strconv.Itoa(service.Port))
		if m.phoneButton(c.Key(service.Port)).FillWidth().MinHeight(64).Padding(14).Gap(12).Radius(14).Background(p.surface).
			Label("打开 " + label).Disabled(m.filesCancel != nil).Children(func() {
			icon(c, "globe", 22, p.ink)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, "端口 "+strconv.Itoa(service.Port)).FontSize(m.font(17)).FontWeight(600)
				ui.Text(c, firstNonEmpty(service.Process, "Web 服务")+" · HTTP").FontSize(m.font(13)).TextColor(p.muted).SingleLine().Ellipsis("…")
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
		return conversationRuntime{}, errors.New("对话运行信息暂时不可用")
	}
	var result conversationRuntime
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return result, err
	}
	if result.View.Key != runtimeKey(ref) {
		return conversationRuntime{}, errors.New("对话运行信息标识不匹配")
	}
	for _, service := range result.Services {
		if service.Host == "" || !validWebServiceHost(service.Host, service.RuntimeToken) || service.Port < 1 || service.Port > 65535 || service.RuntimeToken == "" {
			return conversationRuntime{}, errors.New("无效的运行态服务端点")
		}
	}
	return result, nil
}

func (m *mobileApp) runtimeServiceOwner(service phoneWebService) string {
	if service.Owner == nil {
		return "项目候选 · 未确认对话归属"
	}
	for _, project := range m.state.Projects {
		for _, thread := range project.Threads {
			if thread.ServerID == service.Owner.SessionID && threadAgent(thread) == service.Owner.Backend {
				return "来自对话：" + thread.Title
			}
		}
	}
	return "已核验的对话服务"
}
