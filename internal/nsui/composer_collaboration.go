package nsui

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/nsrequests"
)

func collaborationStatus(cx *core.Scope, c Host, notice *core.Signal[string], panes map[string]*CollaborationPanel) core.Node {
	dispatch := cx.Post
	identity := core.NewMemo(cx, Live(func() string {
		cap := c.CollaborationCapabilities()
		if cap == nil || cap.Version < 1 {
			return ""
		}
		return c.ThreadRef(c.Current()).URL()
	}))
	return core.Dynamic(func(cx *core.Scope) core.Node {
		from := identity.Get()
		if from == "" {
			return core.Group(nil)
		}
		return core.Func(func(cx *core.Scope) core.Node {
			p := panes[from]
			if p == nil {
				p = &CollaborationPanel{From: from, Details: map[string]CollaborationTaskView{}}
				panes[from] = p
			}
			p.Open = false
			viewContext := cx.Context()
			rev := core.NewSignal(cx, 0)
			bump := func() {
				select {
				case <-viewContext.Done():
					return
				default:
					rev.Update(func(n int) int { return n + 1 })
				}
			}
			open := core.NewSignal(cx, false)
			call := collaborationCall(func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
				source, remote := c.State().ReferenceSource, c.State().Remote
				finish := func(raw json.RawMessage, err error) {
					if c.State().ReferenceSource != source {
						err = errors.New(tr("The connected workspace changed; retry from the original workspace"))
					}
					done(raw, err)
					bump()
				}
				if remote != nil {
					go func() {
						var raw json.RawMessage
						err := remote.Call("collaboration", r, &raw)
						dispatch(func() { finish(raw, err) })
					}()
					return
				}
				value, err := c.CollaborationUserOp(r)
				if work, ok := value.(async.Reply); ok && err == nil {
					go func() {
						value, err := work()
						raw, e := json.Marshal(value)
						if err == nil {
							err = e
						}
						dispatch(func() { finish(raw, err) })
					}()
					return
				}
				raw, e := json.Marshal(value)
				if err == nil {
					err = e
				}
				dispatch(func() { finish(raw, err) })
			})
			refresh := func() { p.Refresh(c.CollaborationCapabilities(), call, false) }
			cx.Effect(func() { Live(c.CollaborationCapabilities)(); cx.Untrack(refresh) })
			var poll func()
			poll = func() { refresh(); cx.AfterFunc(time.Second, poll) }
			cx.AfterFunc(time.Second, poll)
			return settingsColumn(core.Show{When: func() bool { rev.Get(); return p.Err != "" || !p.Hidden() }, Then: func(cx *core.Scope) core.Node {
				return composerButton("collaboration-toggle", func() string { rev.Get(); return p.Title() }, func() { p.Open = true; open.Set(true) })
			}},
				ui.Dialog{TestID: "collaboration-dialog", Title: tr("Agent tasks"), Open: open.Get, OnOpenChange: func(v bool, _ base.Reason) { p.Open = v; open.Set(v); bump() }, Children: []core.Node{
					core.Show{When: open.Get, Then: func(cx *core.Scope) core.Node {
						return collaborationContent(cx, c, p, call, notice, bump, func() { rev.Get() })
					}}}})
		})
	})
}

type collaborationHostAdapter struct {
	Host
	notice *core.Signal[string]
}

func (h collaborationHostAdapter) CollaborationCapabilities() *model.CollaborationStatus {
	return Live(h.Host.CollaborationCapabilities)()
}
func (h collaborationHostAdapter) ReadOnly() bool {
	return Live(func() bool { return h.Current().ReadOnly })()
}
func (h collaborationHostAdapter) OpenReference(raw string) {
	openCollaborationThread(h.Host, raw, h.notice)
}
func collaborationContent(cx *core.Scope, c Host, p *CollaborationPanel, call collaborationCall, notice *core.Signal[string], bump func(), read func()) core.Node {
	return nsrequests.CollaborationContent(cx, collaborationHostAdapter{c, notice}, p, call, bump, read)
}

func openCollaborationThread(c Host, raw string, notice *core.Signal[string]) {
	ref, ok := model.ParseThreadURL(raw)
	if ok && ref.Source == c.State().ReferenceSource {
		for _, t := range c.AllThreads() {
			if c.ThreadRef(t) != ref {
				continue
			}
			if p := c.ThreadWorkspace(t.ID); p != c.State().Cwd {
				if err := c.ChangeWorkspace(p); err != nil {
					notice.Set(err.Error())
					return
				}
			}
			c.SelectThread(t)
			return
		}
	}
	notice.Set(tr("Conversation unavailable"))
}
