package desktopui

import (
	context "context"
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	textutil "miya/internal/textutil"
	uiruntime "miya/internal/uiruntime"
	webservice "miya/internal/webservice"
	widgets "miya/internal/widgets"
	net "net"
	strconv "strconv"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func (app *App) runtimeSurface(c *ui.Context, p *panelState, s *PanelSurface) {
	ref := app.C.ThreadRef(app.C.Current())
	if !ref.Valid() {
		panelMessage(c, tr("Background tasks"), tr("Send a message to start a conversation and view runtime information"))
		return
	}
	if s.RuntimeRef != ref {
		s.RuntimeRef = ref
		s.RuntimeData = nil
		s.RuntimeAt = time.Time{}
		s.runtimeLoading = false
	}
	if !s.runtimeLoading && time.Since(s.RuntimeAt) > 5*time.Second {
		app.loadRuntimeSurface(s, ref)
	}
	c.After(time.Second)
	panelBar(c, func() {
		ui.Text(c, tr("Background activity")).FontSize(12).Grow(1)
		if widgets.ToolButton(c, "refresh", tr("Refresh runtime information")).Disabled(s.runtimeLoading).Clicked() {
			s.RuntimeAt = time.Time{}
		}
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(12).Children(func() {
			if s.runtimeLoading && s.RuntimeData == nil {
				ui.Spinner(c)
				return
			}
			if s.runtimeErr != "" {
				ui.Text(c, s.runtimeErr).FontSize(12).TextColor(widgets.Danger)
			}
			if s.RuntimeData == nil {
				return
			}
			view := s.RuntimeData.View
			if view.Notice != "" {
				ui.Text(c, view.Notice).FontSize(12).TextColor(widgets.Muted).LineHeight(1.5)
			}
			ui.Text(c, tr("Background tasks")).FontWeight(600)
			if len(view.Tasks) == 0 {
				ui.Text(c, map[bool]string{true: tr("No native background tasks"), false: tr("Native task list unconfirmed")}[view.TasksKnown]).FontSize(12).TextColor(widgets.Muted)
			}
			for _, task := range view.Tasks {
				ui.Column(c.Key("task-" + task.ID)).FillWidth().Padding(12).Gap(6).Radius(8).Background(widgets.Panel).Children(func() {
					ui.Text(c, textutil.FirstNonEmpty(task.Description, tr("Background tasks"))).FontSize(12).LineHeight(1.4)
					ui.Text(c, widgets.RuntimeStatus(task.Status)+" · "+widgets.RuntimeKind(task.Kind)).FontSize(11).TextColor(widgets.Muted)
				})
			}
			ui.Text(c, tr("System process")).FontWeight(600)
			if len(view.Processes) == 0 {
				ui.Text(c, tr("No process identity available for verification")).FontSize(12).TextColor(widgets.Muted)
			}
			for _, process := range view.Processes {
				ui.Text(c, fmt.Sprintf("%s · PID %d · %s", process.Name, process.Identity.PID, widgets.RuntimeStatus(process.Status))).FontSize(12).TextColor(widgets.Muted)
			}
			ui.Text(c, tr("Web services")).FontWeight(600)
			if len(s.RuntimeData.Services) == 0 {
				ui.Text(c, tr("No verified HTTP service for this conversation")).FontSize(12).TextColor(widgets.Muted)
			}
			for _, service := range s.RuntimeData.Services {
				if ui.Button(c, fmt.Sprintf(tr("Open port %d"), service.Port)).FillWidth().MinHeight(40).Clicked() {
					app.openRuntimeService(p, s, ref, service)
				}
			}
			for _, endpoint := range view.Endpoints {
				if endpoint.Protocol != "http" {
					ui.Text(c, fmt.Sprintf("%s:%d · TCP", endpoint.Host, endpoint.Port)).FontSize(11).TextColor(widgets.Muted)
				}
			}
			if view.ObservedAt > 0 {
				ui.Text(c, tr("Last verified ")+time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")).FontSize(11).TextColor(widgets.Muted)
			}
		})
	})
}

func (app *App) loadRuntimeSurface(s *PanelSurface, ref appmodel.ThreadRef) {
	s.runtimeLoading = true
	s.RuntimeAt = time.Now()
	project := app.C.ThreadWorkspace(app.C.Current().ID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		result, err := app.C.ReadRuntime(ctx, ref, project)
		uiruntime.RunOnMain(func() {
			if s.closed || s.RuntimeRef != ref {
				return
			}
			s.runtimeLoading = false
			s.runtimeErr = ""
			if err != nil {
				s.runtimeErr = err.Error()
				if s.RuntimeData != nil {
					widgets.RuntimeObservationLost(s.RuntimeData)
				}
			} else {
				s.RuntimeData = &result
			}
		})
	}()
}

func (app *App) openRuntimeService(p *panelState, s *PanelSurface, ref appmodel.ThreadRef, service webservice.Service) {
	cl := app.C.State().Remote
	go func() {
		var err error
		if cl != nil {
			err = cl.Call("runtimeValidate", service, nil)
		} else {
			err = app.C.Runtimes().ValidateToken(context.Background(), service.RuntimeToken, service.Host, service.Port)
		}
		uiruntime.RunOnMain(func() {
			if s.closed || s.RuntimeRef != ref {
				return
			}
			if err != nil {
				s.runtimeErr = err.Error()
				return
			}
			app.openSurface(p, surfaceBrowser)
			browser := p.Surfaces[p.Active]
			browser.address = "http://" + net.JoinHostPort(service.Host, strconv.Itoa(service.Port)) + "/"
			if app.C.RemoteHost() {
				browser.loaded = browser.address
				app.navigateRemoteBrowser(browser, browser.address, service)
				return
			}
			app.navigate(browser)
		})
	}()
}
