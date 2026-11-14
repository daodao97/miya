package nsui

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	"miya/internal/runtime/presentation"
	webservice "miya/internal/runtime/web"
	"miya/internal/textutil"
)

func (p *panel) runtimeView(cx *core.Scope, s *surface) core.Node {
	cx.Effect(func() {
		ref := p.ref.Get()
		cx.Untrack(func() {
			if s.runtimeRef != ref {
				s.runtimeRef = ref
				s.runtimeData.Set(nil)
				s.runtimeBusy.Set(false)
				s.runtimeErr.Set("")
			}
			p.loadRuntime(s)
		})
	})
	var poll func()
	poll = func() { p.loadRuntime(s); cx.AfterFunc(5*time.Second, poll) }
	cx.AfterFunc(5*time.Second, poll)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if !p.ref.Get().Valid() {
			return panelMessage(cx, tr("Background tasks"), tr("Send a message to start a conversation and view runtime information"))
		}
		return core.View{TestID: "runtime", Style: ui.FillRest, Children: []core.Node{
			panelBar(cx,
				core.Text{Content: tr("Background activity"), Style: core.Style{FontSize: 12, FlexGrow: 1}},
				ui.IconButton{TestID: "runtime-refresh", Icon: nswidgets.IconOf("refresh"), Label: tr("Refresh runtime information"), Disabled: s.runtimeBusy.Get, OnPress: func() { p.loadRuntime(s) }},
			),
			core.Scroll{Style: ui.FillRest, Children: []core.Node{
				core.View{Style: core.Style{Padding: core.All(16), Gap: 12}, Children: []core.Node{
					core.Show{When: func() bool { return s.runtimeBusy.Get() && s.runtimeData.Get() == nil }, Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 16} }},
					core.Show{When: func() bool { return s.runtimeErr.Get() != "" }, Then: func(cx *core.Scope) core.Node { return panelSmallText(cx, s.runtimeErr.Get, danger) }},
					core.Dynamic(func(cx *core.Scope) core.Node { return p.runtimeResult(cx, s) }),
				}},
			}},
		}}
	})
}

func (p *panel) runtimeResult(cx *core.Scope, s *surface) core.Node {
	data := s.runtimeData.Get()
	if data == nil {
		return core.Group(nil)
	}
	view := data.View
	text := func(value string) core.Node { return nswidgets.MutedText(cx, func() string { return value }) }
	heading := func(value string) core.Node {
		return core.Text{Content: value, Style: core.Style{FontSize: 13, FontWeight: ui.Semibold}}
	}
	var nodes []core.Node
	if view.Notice != "" {
		nodes = append(nodes, text(view.Notice))
	}
	nodes = append(nodes, heading(tr("Background tasks")))
	if len(view.Tasks) == 0 {
		value := tr("Native task list unconfirmed")
		if view.TasksKnown {
			value = tr("No native background tasks")
		}
		nodes = append(nodes, text(value))
	}
	for _, task := range view.Tasks {
		nodes = append(nodes, core.View{TestID: "runtime-task-" + task.ID, Style: core.Style{Padding: core.All(12), Gap: 6, CornerRadius: 8},
			StyleFunc: func() core.Style { return core.Style{Background: nswidgets.ColorsOf(cx).Panel()} }, Children: []core.Node{
				core.Text{Content: textutil.FirstNonEmpty(task.Description, tr("Background tasks")), Style: core.Style{FontSize: 12}},
				text(presentation.RuntimeStatus(task.Status) + " · " + presentation.RuntimeKind(task.Kind)),
			}})
	}
	nodes = append(nodes, heading(tr("System process")))
	if len(view.Processes) == 0 {
		nodes = append(nodes, text(tr("No process identity available for verification")))
	}
	for _, process := range view.Processes {
		nodes = append(nodes, text(fmt.Sprintf("%s · PID %d · %s", process.Name, process.Identity.PID, presentation.RuntimeStatus(process.Status))))
	}
	nodes = append(nodes, heading(tr("Web services")))
	if len(data.Services) == 0 {
		nodes = append(nodes, text(tr("No verified HTTP service for this conversation")))
	}
	for _, service := range data.Services {
		nodes = append(nodes, ui.Button{TestID: "runtime-open-" + strconv.Itoa(service.Port), Text: fmt.Sprintf(tr("Open port %d"), service.Port),
			OnPress: func() { p.openRuntimeService(s, s.runtimeRef, service) }})
	}
	for _, endpoint := range view.Endpoints {
		if endpoint.Protocol != "http" {
			nodes = append(nodes, text(fmt.Sprintf("%s:%d · TCP", endpoint.Host, endpoint.Port)))
		}
	}
	if view.ObservedAt > 0 {
		nodes = append(nodes, text(tr("Last verified ")+time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")))
	}
	return core.Group(nodes)
}

func (p *panel) loadRuntime(s *surface) {
	ref := p.ref.GetUntracked()
	if s.closed || !p.open.GetUntracked() || !ref.Valid() || s.runtimeBusy.GetUntracked() {
		return
	}
	s.runtimeBusy.Set(true)
	project := p.host.ThreadWorkspace(p.threadID)
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 12*time.Second)
		defer cancel()
		result, err := p.host.ReadRuntime(ctx, ref, project)
		p.cx.Post(func() {
			if s.closed || s.runtimeRef != ref {
				return
			}
			s.runtimeBusy.Set(false)
			s.runtimeErr.Set("")
			if err != nil {
				s.runtimeErr.Set(err.Error())
				if old := s.runtimeData.GetUntracked(); old != nil {
					stale := *old
					stale.View.Tasks = slices.Clone(old.View.Tasks)
					stale.View.Processes = slices.Clone(old.View.Processes)
					presentation.RuntimeObservationLost(&stale)
					s.runtimeData.Set(&stale)
				}
			} else {
				s.runtimeData.Set(&result)
			}
		})
	}()
}

func (p *panel) openRuntimeService(s *surface, ref model.ThreadRef, service webservice.Service) {
	cl := p.host.State().Remote
	var validate func(context.Context, string, string, int) error
	if cl == nil {
		if runtimes := p.host.Runtimes(); runtimes != nil {
			validate = runtimes.ValidateToken
		}
	}
	go func() {
		var err error
		if cl != nil {
			err = cl.Call("runtimeValidate", service, nil)
		} else if validate != nil {
			err = validate(s.ctx, service.RuntimeToken, service.Host, service.Port)
		} else {
			err = fmt.Errorf("%s", tr("No verified HTTP service for this conversation"))
		}
		p.cx.Post(func() {
			if s.closed || s.runtimeRef != ref {
				return
			}
			if err != nil {
				s.runtimeErr.Set(err.Error())
				return
			}
			p.openSurface(surfaceBrowser)
			browser := p.findActive()
			browser.address.Set("http://" + net.JoinHostPort(service.Host, strconv.Itoa(service.Port)) + "/")
			if p.host.RemoteHost() {
				p.navigateRemoteBrowser(browser, browser.address.GetUntracked(), service)
			} else {
				p.navigate(browser)
			}
		})
	}()
}

func (p *panel) findActive() *surface {
	for _, s := range p.surfaces.GetUntracked() {
		if s.id == p.active.GetUntracked() {
			return s
		}
	}
	return nil
}
