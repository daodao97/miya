package nsui

import (
	"strconv"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/chat/subagent"
	"miya/internal/nswidgets"
	svcproto "miya/internal/service/protocol"
)

func taskStatus(cx *core.Scope, c Host, notice *core.Signal[string]) core.Node {
	thread := Live(c.Current)
	return core.View{TestID: "task-status", Children: []core.Node{
		core.Each(func() []model.ThreadTask { return thread().Tasks }, func(t model.ThreadTask) string { return t.ID }, func(cx *core.Scope, get func() model.ThreadTask, _ func() int) core.Node {
			id := core.Untracked(cx, func() string { return get().ID })
			pending := core.NewSignal(cx, false)
			button := func(label, action, icon string) core.Node {
				disabled := func() bool { t := thread(); return pending.Get() || t.ReadOnly || t.TaskPending || t.TaskStopping }
				return ui.IconButton{TestID: "task-" + action + "-" + id, Label: label, Icon: nswidgets.IconOf(icon), Disabled: disabled, OnPress: func() {
					threadID := c.Current().ID
					if get().Kind != "follow_up" {
						notice.Set(c.TaskAction(threadID, id, action))
						return
					}
					pending.Set(true)
					finish := func(value any, err error) {
						pending.Set(false)
						if c.Current().ID == threadID {
							message, _ := value.(string)
							if err != nil {
								message = err.Error()
							}
							notice.Set(message)
						}
					}
					if cl := c.State().Remote; cl != nil {
						go func() {
							var message string
							err := cl.Call("task", svcproto.TaskAction{Thread: threadID, Task: id, Action: action}, &message)
							cx.Post(func() { finish(message, err) })
						}()
						return
					}
					switch reply := c.PrepareTaskAction(threadID, id, action).(type) {
					case async.Reply:
						go func() {
							value, err := reply()
							cx.Post(func() { finish(value, err) })
						}()
					default:
						finish(reply, nil)
					}
				}}
			}
			return core.View{StyleFunc: nswidgets.CardStyle(cx), Children: []core.Node{
				core.Text{ContentFunc: func() string {
					kind := tr("Goal")
					switch get().Kind {
					case "loop":
						kind = tr("Loop")
					case "follow_up":
						kind = tr("Follow-up")
					}
					return kind + " · " + appmodel.TaskStatusLabel(thread(), get(), c.TaskWaitingApproval(thread())) + " · " + get().Title
				}},
				nswidgets.MutedText(cx, func() string { return appmodel.TaskDetail(get()) }),
				core.Dynamic(func(cx *core.Scope) core.Node {
					t := get()
					var actions []core.Node
					if t.Kind == "follow_up" {
						if t.Status == "blocked" {
							actions = append(actions, button(tr("Dismiss follow-up"), "dismiss", "trash-2"))
						} else {
							actions = append(actions, button(tr("Cancel follow-up"), "cancel", "x"))
						}
					} else if t.Kind == "goal" {
						switch t.Status {
						case "active":
							actions = append(actions, button(tr("Pause goal"), "pause", "player-pause"))
						case "paused", "blocked", "usageLimited", "budgetLimited":
							actions = append(actions, button(tr("Resume goal"), "resume", "player-play"))
						}
						actions = append(actions, button(tr("Clear goal"), "clear", "trash-2"))
					} else if t.Status == "waiting" || t.Status == "running" {
						label := tr("Stop loop")
						n := 0
						for _, task := range thread().Tasks {
							if task.Kind == "loop" && (task.Status == "waiting" || task.Status == "running") {
								n++
							}
						}
						if n > 1 {
							label = tr("Stop all loops")
						}
						actions = append(actions, button(label, "stop", "x"))
					} else if model.LoopFinished(t) {
						actions = append(actions, button(tr("Clear loop"), "clear", "trash-2"))
					}
					return settingsActions(actions...)
				}),
			}}
		}),
		core.Each(func() []model.SubAgent {
			t := thread()
			if t.Busy {
				return subagent.ActiveAgents(t.Messages)
			}
			return nil
		}, func(a model.SubAgent) string { return a.ID }, func(cx *core.Scope, get func() model.SubAgent, _ func() int) core.Node {
			id := core.Untracked(cx, func() string { return get().ID })
			return core.Text{TestID: "active-subagent-" + id, ContentFunc: func() string {
				a := get()
				return tr("Subagent") + " · " + subagent.StatusLabel(a.Status) + " · " + a.Name + " · " + strconv.Itoa(a.Steps) + " · " + a.Detail
			}, Style: core.Style{FontSize: 12}}
		}),
	}}
}
