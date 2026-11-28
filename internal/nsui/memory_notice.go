package nsui

import (
	"encoding/json"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/memory/manager"
)

func desktopMemoryNotice(cx *core.Scope, c Host, notice model.MemoryNotice) core.Node {
	expanded := core.NewSignal(cx, false)
	scope := tr("Current project")
	if notice.Scope == "user" {
		scope = tr("User preferences")
	}
	req := manager.Request{Project: notice.Project, Scope: notice.Scope, ID: notice.ID, Version: notice.Version}
	return ui.Accordion{
		Open: func(int) bool { return expanded.Get() }, OnOpenChange: func(_ int, open bool) { expanded.Set(open) },
		Items: []ui.AccordionItem{{Text: tr("Miya memory changed") + " · " + scope,
			TestID: "memory-notice-" + notice.ID, Content: core.Show{
				When: expanded.Get, Then: func(cx *core.Scope) core.Node {
					entry := core.NewSignal[*memory.Entry](cx, nil)
					message := core.NewSignal(cx, "")
					busy := core.NewSignal(cx, true)
					load := func() {
						callDesktopMemory(cx, c, "memoryGet", req, func(raw json.RawMessage, err error) {
							busy.Set(false)
							var next memory.Entry
							if err == nil {
								err = json.Unmarshal(raw, &next)
							}
							if err != nil {
								message.Set(err.Error())
								return
							}
							message.Set("")
							entry.Set(&next)
						})
					}
					cx.Post(load)
					return settingsColumn(
						core.Dynamic(func(cx *core.Scope) core.Node {
							e := entry.Get()
							if e == nil || e.ID == "" {
								return core.Show{When: busy.Get, Then: func(*core.Scope) core.Node { return core.Text{Content: tr("Loading memory…")} }}
							}
							var actions []core.Node
							if e.Version == notice.Version && (e.State == memory.EntryActive || e.State == memory.EntryExpired || e.State == memory.EntryConflicted) {
								actions = append(actions, settingsActions(settingsButton("memory-notice-undo-"+notice.ID, tr("Undo memory change"), func() {
									busy.Set(true)
									callDesktopMemory(cx, c, "memoryForget", req, func(_ json.RawMessage, err error) {
										if err != nil {
											busy.Set(false)
											message.Set(err.Error())
											return
										}
										load()
									})
								}, busy.Get)))
							}
							return settingsColumn(append([]core.Node{core.Text{Content: e.Content, Selectable: core.SelectableOn},
								settingsText(cx, func() string { return memoryState(e.State) })}, actions...)...)
						}),
						settingsText(cx, message.Get),
					)
				},
			}}},
	}
}
