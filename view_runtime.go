package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/textutil"
	"miya/internal/webservice"
)

func (app *chatApp) runtimeSurface(c *ui.Context, p *panelState, s *panelSurface) {
	ref := app.c.threadRef(app.c.current())
	if !ref.Valid() {
		panelMessage(c, tr("Background tasks"), tr("Send a message to start a conversation and view runtime information"))
		return
	}
	if s.runtimeRef != ref {
		s.runtimeRef = ref
		s.runtimeData = nil
		s.runtimeAt = time.Time{}
		s.runtimeLoading = false
	}
	if !s.runtimeLoading && time.Since(s.runtimeAt) > 5*time.Second {
		app.loadRuntimeSurface(s, ref)
	}
	c.After(time.Second)
	panelBar(c, func() {
		ui.Text(c, tr("Background activity")).FontSize(12).Grow(1)
		if toolButton(c, "refresh", tr("Refresh runtime information")).Disabled(s.runtimeLoading).Clicked() {
			s.runtimeAt = time.Time{}
		}
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(12).Children(func() {
			if s.runtimeLoading && s.runtimeData == nil {
				ui.Spinner(c)
				return
			}
			if s.runtimeErr != "" {
				ui.Text(c, s.runtimeErr).FontSize(12).TextColor(danger)
			}
			if s.runtimeData == nil {
				return
			}
			view := s.runtimeData.View
			if view.Notice != "" {
				ui.Text(c, view.Notice).FontSize(12).TextColor(muted).LineHeight(1.5)
			}
			ui.Text(c, tr("Background tasks")).FontWeight(600)
			if len(view.Tasks) == 0 {
				ui.Text(c, map[bool]string{true: tr("No native background tasks"), false: tr("Native task list unconfirmed")}[view.TasksKnown]).FontSize(12).TextColor(muted)
			}
			for _, task := range view.Tasks {
				ui.Column(c.Key("task-" + task.ID)).FillWidth().Padding(12).Gap(6).Radius(8).Background(panel).Children(func() {
					ui.Text(c, textutil.FirstNonEmpty(task.Description, tr("Background tasks"))).FontSize(12).LineHeight(1.4)
					ui.Text(c, runtimeStatus(task.Status)+" · "+runtimeKind(task.Kind)).FontSize(11).TextColor(muted)
				})
			}
			ui.Text(c, tr("System process")).FontWeight(600)
			if len(view.Processes) == 0 {
				ui.Text(c, tr("No process identity available for verification")).FontSize(12).TextColor(muted)
			}
			for _, process := range view.Processes {
				ui.Text(c, fmt.Sprintf("%s · PID %d · %s", process.Name, process.Identity.PID, runtimeStatus(process.Status))).FontSize(12).TextColor(muted)
			}
			ui.Text(c, tr("Web services")).FontWeight(600)
			if len(s.runtimeData.Services) == 0 {
				ui.Text(c, tr("No verified HTTP service for this conversation")).FontSize(12).TextColor(muted)
			}
			for _, service := range s.runtimeData.Services {
				if ui.Button(c, fmt.Sprintf(tr("Open port %d"), service.Port)).FillWidth().MinHeight(40).Clicked() {
					app.openRuntimeService(p, s, ref, service)
				}
			}
			for _, endpoint := range view.Endpoints {
				if endpoint.Protocol != "http" {
					ui.Text(c, fmt.Sprintf("%s:%d · TCP", endpoint.Host, endpoint.Port)).FontSize(11).TextColor(muted)
				}
			}
			if view.ObservedAt > 0 {
				ui.Text(c, tr("Last verified ")+time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")).FontSize(11).TextColor(muted)
			}
		})
	})
}
func (app *chatApp) loadRuntimeSurface(s *panelSurface, ref ThreadRef) {
	s.runtimeLoading = true
	s.runtimeAt = time.Now()
	project := app.c.threadWorkspace(app.c.current().ID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		result, err := app.c.readRuntime(ctx, ref, project)
		runOnMain(func() {
			if s.closed || s.runtimeRef != ref {
				return
			}
			s.runtimeLoading = false
			s.runtimeErr = ""
			if err != nil {
				s.runtimeErr = err.Error()
				if s.runtimeData != nil {
					runtimeObservationLost(s.runtimeData)
				}
			} else {
				s.runtimeData = &result
			}
		})
	}()
}
func (app *chatApp) openRuntimeService(p *panelState, s *panelSurface, ref ThreadRef, service webservice.Service) {
	cl := app.c.remote
	go func() {
		var err error
		if cl != nil {
			err = cl.Call("runtimeValidate", service, nil)
		} else {
			err = app.c.runtimes().ValidateToken(context.Background(), service.RuntimeToken, service.Host, service.Port)
		}
		runOnMain(func() {
			if s.closed || s.runtimeRef != ref {
				return
			}
			if err != nil {
				s.runtimeErr = err.Error()
				return
			}
			app.openSurface(p, surfaceBrowser)
			browser := p.surfaces[p.active]
			browser.address = "http://" + net.JoinHostPort(service.Host, strconv.Itoa(service.Port)) + "/"
			if app.c.remoteHost() {
				browser.loaded = browser.address
				app.navigateRemoteBrowser(browser, browser.address, service)
				return
			}
			app.navigate(browser)
		})
	}()
}
