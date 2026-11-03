package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
)

func (app *chatApp) runtimeSurface(c *ui.Context, p *panelState, s *panelSurface) {
	ref := app.c.threadRef(app.c.current())
	if !ref.valid() {
		panelMessage(c, "后台运行", "发送消息并建立对话后可查看运行信息")
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
		ui.Text(c, "本对话后台运行").FontSize(12).Grow(1)
		if toolButton(c, "refresh", "刷新运行信息").Disabled(s.runtimeLoading).Clicked() {
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
			ui.Text(c, "后台任务").FontWeight(600)
			if len(view.Tasks) == 0 {
				ui.Text(c, map[bool]string{true: "当前没有原生后台任务", false: "原生任务集合尚未确认"}[view.TasksKnown]).FontSize(12).TextColor(muted)
			}
			for _, task := range view.Tasks {
				ui.Column(c.Key("task-" + task.ID)).FillWidth().Padding(12).Gap(6).Radius(8).Background(panel).Children(func() {
					ui.Text(c, firstNonEmpty(task.Description, "后台任务")).FontSize(12).LineHeight(1.4)
					ui.Text(c, runtimeStatus(task.Status)+" · "+runtimeKind(task.Kind)).FontSize(11).TextColor(muted)
				})
			}
			ui.Text(c, "系统进程").FontWeight(600)
			if len(view.Processes) == 0 {
				ui.Text(c, "尚无可核验的进程身份").FontSize(12).TextColor(muted)
			}
			for _, process := range view.Processes {
				ui.Text(c, fmt.Sprintf("%s · PID %d · %s", process.Name, process.Identity.PID, runtimeStatus(process.Status))).FontSize(12).TextColor(muted)
			}
			ui.Text(c, "Web 服务").FontWeight(600)
			if len(s.runtimeData.Services) == 0 {
				ui.Text(c, "尚未核验到本对话的 HTTP 服务").FontSize(12).TextColor(muted)
			}
			for _, service := range s.runtimeData.Services {
				if ui.Button(c, fmt.Sprintf("打开端口 %d", service.Port)).FillWidth().MinHeight(40).Clicked() {
					app.openRuntimeService(p, s, ref, service)
				}
			}
			for _, endpoint := range view.Endpoints {
				if endpoint.Protocol != "http" {
					ui.Text(c, fmt.Sprintf("%s:%d · TCP", endpoint.Host, endpoint.Port)).FontSize(11).TextColor(muted)
				}
			}
			if view.ObservedAt > 0 {
				ui.Text(c, "最近核验 "+time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")).FontSize(11).TextColor(muted)
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
func (app *chatApp) openRuntimeService(p *panelState, s *panelSurface, ref ThreadRef, service phoneWebService) {
	go func() {
		var err error
		if app.c.remote != nil {
			err = app.c.remote.call("runtimeValidate", service, nil)
		} else {
			err = app.c.runtimes().validateToken(context.Background(), service.RuntimeToken, service.Host, service.Port)
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
			app.navigate(browser)
		})
	}()
}
