package nsui

import (
	"encoding/json"
	"slices"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/memory"
	"miya/internal/memory/manager"
)

func botProfile(cx *core.Scope, c Host, b bot.Bot, back func(), showAutomations func(automationShortcut)) core.Node {
	duties := core.NewSignal[*[]automation.View](cx, nil)
	dutyError := core.NewSignal(cx, "")
	memories := core.NewSignal[*manager.List](cx, nil)
	memoryError := core.NewSignal(cx, "")
	busy := core.NewSignal(cx, true)
	req := manager.Request{Project: b.Project, Scope: "bot", Bot: b.ID}
	loadMemory := func() {
		busy.Set(true)
		memoryError.Set("")
		callDesktopMemory(cx, c, "memoryList", req, func(raw json.RawMessage, err error) {
			busy.Set(false)
			var result manager.List
			if err == nil {
				err = json.Unmarshal(raw, &result)
			}
			if err != nil {
				memoryError.Set(err.Error())
				return
			}
			memories.Set(&result)
		})
	}
	cx.Post(func() {
		loadMemory()
		callAutomationService(cx, c, "automationList", automation.Request{}, func(raw json.RawMessage, err error) {
			var result []automation.View
			if err == nil {
				err = json.Unmarshal(raw, &result)
			}
			if err != nil {
				dutyError.Set(err.Error())
				return
			}
			result = slices.DeleteFunc(result, func(v automation.View) bool { return v.Bot != b.ID })
			duties.Set(&result)
		})
	})
	return autoColumn(
		settingsActions(autoButton("bot-profile-back", "Back", back), autoHeading(b.Name)),
		autoMuted(cx, b.Note),
		autoCard(cx, settingsActions(settingsSectionTitle(tr("Duties")), autoButton("bot-add-duty", "Add a duty", func() {
			showAutomations(automationShortcut{duty: &b})
		})), settingsText(cx, dutyError.Get), core.Dynamic(func(cx *core.Scope) core.Node {
			items := duties.Get()
			if items == nil {
				return nil
			}
			if len(*items) == 0 {
				return autoMuted(cx, tr("No duties yet. Give it an automation and it works on its own."))
			}
			var rows []core.Node
			for _, v := range *items {
				rows = append(rows, ui.FormRow{TestID: "bot-duty-" + v.ID, Title: v.Name, Detail: v.ScheduleLabel(),
					OnPress: func() { showAutomations(automationShortcut{detail: v.ID}) }})
			}
			return ui.FormSection{Children: rows}
		})),
		autoCard(cx, settingsSectionTitle(tr("Memory")), settingsText(cx, memoryError.Get), core.Dynamic(func(cx *core.Scope) core.Node {
			list := memories.Get()
			if list == nil {
				return core.Show{When: busy.Get, Then: func(*core.Scope) core.Node { return ui.Spinner{} }}
			}
			var rows []core.Node
			for _, e := range list.Entries {
				if e.State != memory.EntryActive {
					continue
				}
				rows = append(rows, ui.FormRow{Title: e.Content, Control: settingsButton("bot-forget-"+e.ID, tr("Forget"), func() {
					request := req
					request.ID, request.Version = e.ID, e.Version
					busy.Set(true)
					memoryError.Set("")
					callDesktopMemory(cx, c, "memoryForget", request, func(_ json.RawMessage, err error) {
						if err != nil {
							busy.Set(false)
							memoryError.Set(err.Error())
							return
						}
						loadMemory()
					})
				}, busy.Get)})
			}
			if len(rows) == 0 {
				return autoMuted(cx, tr("Nothing yet. Tell it what to remember about this work."))
			}
			return ui.FormSection{Children: rows}
		})),
	)
}
