package nsui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	memorypresentation "miya/internal/memory/presentation"
	"miya/internal/nswidgets"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

type settingsMemory struct {
	loaded                                   bool
	promotionContent, promotionFile          string
	onSource                                 func()
	cx                                       *core.Scope
	c                                        Host
	revision                                 *core.Signal[int]
	scope                                    *core.Signal[string]
	filter, kind, content, task, until, file *core.Signal[string]
	project                                  string
	client                                   *svcclient.Client
	generation                               int
	list                                     manager.List
	loading, busy, editing                   bool
	err, id, confirmDelete                   string
	version                                  uint64
	promotion                                *manager.Promotion
	stopPoll                                 func() bool
}

func newSettingsMemory(cx *core.Scope, c Host, category *core.Signal[string]) *settingsMemory {
	m := &settingsMemory{cx: cx, c: c, revision: core.NewSignal(cx, 0), scope: core.NewSignal(cx, "project"), filter: core.NewSignal(cx, ""), kind: core.NewSignal(cx, "fact"), content: core.NewSignal(cx, ""), task: core.NewSignal(cx, ""), until: core.NewSignal(cx, ""), file: core.NewSignal(cx, "AGENTS.md")}
	key := core.NewMemo(cx, Live(func() struct {
		selected, scope, project string
		client                   *svcclient.Client
		revision                 uint64
	} {
		return struct {
			selected, scope, project string
			client                   *svcclient.Client
			revision                 uint64
		}{category.Get(), m.scope.Get(), c.State().Cwd, c.State().Remote, c.State().MemoryRevision}
	}))
	cx.Effect(func() {
		k := key.Get()
		selected, scope, project, client := k.selected, k.scope, k.project, k.client
		cx.Untrack(func() {
			if m.project != project || m.client != client {
				m.generation++
				m.project, m.client = project, client
				m.list = manager.List{}
				m.loaded = false
				m.loading, m.busy, m.editing = false, false, false
				m.err, m.id, m.confirmDelete = "", "", ""
				m.promotion = nil
				m.changed()
			}
			if selected == "memory" && !m.busy && !m.editing && !(scope != "user" && manager.RootProject(project)) {
				m.load()
			}
		})
	})
	return m
}
func (m *settingsMemory) changed() { m.revision.Update(func(n int) int { return n + 1 }) }
func (m *settingsMemory) request() manager.Request {
	return manager.Request{Project: m.project, Scope: m.scope.Get(), Thread: m.c.Current().ID}
}
func (m *settingsMemory) call(op string, r manager.Request, done func(json.RawMessage, error)) {
	gen, cl := m.generation, m.client
	callDesktopMemory(m.cx, m.c, op, r, func(raw json.RawMessage, err error) {
		if gen == m.generation && m.project == m.c.State().Cwd && cl == m.c.State().Remote {
			done(raw, err)
		}
	})
}

func callDesktopMemory(cx *core.Scope, c Host, op string, r manager.Request, done func(json.RawMessage, error)) {
	finish := func(v any, err error) {
		var raw json.RawMessage
		if err == nil && v != nil {
			raw, err = json.Marshal(v)
		}
		done(raw, err)
	}
	if cl := c.State().Remote; cl != nil {
		go func() {
			var raw json.RawMessage
			err := cl.Call(op, r, &raw)
			cx.Post(func() {
				if cl != c.State().Remote {
					done(nil, svcproto.ErrClosed)
					return
				}
				done(raw, err)
			})
		}()
		return
	}
	mg := c.Memories()
	if mg == nil {
		finish(nil, fmt.Errorf("%s", tr("Memory requires the Miya background service")))
		return
	}
	v, err := mg.Op(op, r)
	if later, ok := v.(async.Reply); ok && err == nil {
		go func() { v, err := later(); cx.Post(func() { finish(v, err) }) }()
		return
	}
	finish(v, err)
}
func (m *settingsMemory) load() {
	if m.loading || m.busy || m.scope.Get() != "user" && manager.RootProject(m.project) {
		return
	}
	if m.stopPoll != nil {
		m.stopPoll()
	}
	m.loading = true
	m.changed()
	m.call("memoryList", m.request(), func(raw json.RawMessage, err error) {
		m.loading = false
		if err == nil {
			err = json.Unmarshal(raw, &m.list)
		}
		if err != nil {
			m.list = manager.List{}
			m.loaded = false
			m.err = err.Error()
		} else {
			m.loaded = true
			m.err = ""
		}
		m.changed()
		m.stopPoll = m.cx.AfterFunc(5*time.Second, func() {
			if !m.editing {
				m.load()
			}
		})
	})
}
func (m *settingsMemory) action(op string, r manager.Request, done func(json.RawMessage)) {
	if m.busy || m.loading || !m.loaded {
		return
	}
	m.busy = true
	m.err = ""
	m.changed()
	m.call(op, r, func(raw json.RawMessage, err error) {
		m.busy = false
		if err != nil {
			m.err = err.Error()
			m.changed()
			return
		}
		if done != nil {
			done(raw)
		}
		m.changed()
		m.load()
	})
}
func (m *settingsMemory) switchScope(v string) {
	if m.busy || m.editing {
		return
	}
	m.generation++
	m.scope.Set(v)
	m.list = manager.List{}
	m.loaded = false
	m.loading = false
	m.promotion = nil
	m.confirmDelete = ""
	m.changed()
	m.load()
}
func (m *settingsMemory) groups() []ui.SettingsGroup {
	return []ui.SettingsGroup{{ID: "memory", Title: tr("Shared memory"), Items: []ui.SettingsItem{{ID: "scope", Title: tr("Memory scope"), Control: settingsSelect{TestID: "memory-scope", Label: tr("Memory scope"), Options: []ui.Option[string]{{Value: "project", Label: tr("Current project")}, {Value: "user", Label: tr("User preferences")}}, Value: m.scope.Get, OnChange: m.switchScope, Disabled: func() bool { m.revision.Get(); return m.busy || m.editing }}}, {ID: "memory", Title: tr("Project settings"), Content: core.Func(func(cx *core.Scope) core.Node {
		cx.Post(m.load)
		return core.Dynamic(func(cx *core.Scope) core.Node {
			m.revision.Get()
			scope := m.scope.Get()
			var nodes []core.Node
			if scope != "user" && manager.RootProject(m.project) {
				return settingsText(cx, func() string {
					return tr("Open a project first. Project memory and its automatic settings belong to a project folder, not the filesystem root.")
				})
			}
			if m.err != "" {
				nodes = append(nodes, settingsText(cx, func() string { return m.err }))
			}
			if m.editing {
				return settingsColumn(append(nodes, m.form(cx))...)
			}
			refresh := settingsButton("memory-refresh", tr("Refresh"), m.load, func() bool { m.revision.Get(); return m.loading || m.busy })
			if !m.loaded {
				if m.loading {
					nodes = append(nodes, settingsText(cx, func() string { return tr("Loading…") }))
				}
				return settingsColumn(append(nodes, settingsActions(refresh))...)
			}
			nodes = append(nodes, settingsSectionTitle(tr("Memories")), settingsActions(ui.Input{TestID: "memory-filter", Label: tr("Filter memories"), Placeholder: tr("Filter content, type, or status"), Value: m.filter.Get, OnChange: m.filter.Set, Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(180)}}, refresh, ui.Button{TestID: "memory-add", Text: tr("Add memory"), Icon: nswidgets.IconOf("plus"), OnPress: func() {
				m.edit(memory.Entry{})
				if scope == "user" {
					m.kind.Set("preference")
				}
			}, Disabled: func() bool { m.revision.Get(); return m.busy || m.loading || !m.loaded }}), core.Dynamic(func(cx *core.Scope) core.Node {
				query := strings.ToLower(m.filter.Get())
				var entries []core.Node
				for _, e := range m.list.Entries {
					if query != "" && !strings.Contains(strings.ToLower(e.Content+" "+e.Kind+" "+e.State), query) {
						continue
					}
					entries = append(entries, m.row(cx, e))
				}
				if len(entries) == 0 {
					message := tr("No matching memories.")
					if len(m.list.Entries) == 0 {
						message = tr("No memories yet. Add one here or explicitly ask an agent to remember something in a conversation.")
					}
					return settingsText(cx, func() string { return message })
				}
				return settingsColumn(entries...)
			}))
			if scope != "user" {
				nodes = append(nodes, m.projectSettings(cx))
			}
			return settingsColumn(nodes...)
		})
	})}}}}
}
func (m *settingsMemory) projectSettings(cx *core.Scope) core.Node {
	var nodes []core.Node
	disabled := func() bool { m.revision.Get(); return m.busy || m.loading || !m.loaded }
	toggle := func(id, title, help string, get func(memory.Settings) bool, set func(*memory.Settings, bool), extra func() bool) {
		on := func() bool { m.revision.Get(); return get(m.list.Settings) }
		dis := func() bool { return disabled() || extra != nil && extra() }
		nodes = append(nodes, ui.FormRow{Title: tr(title), Detail: tr(help), Control: settingsToggle(id, tr(title), on, func(v bool) {
			s := m.list.Settings
			set(&s, v)
			r := m.request()
			r.Settings = &s
			m.action("memoryConfigure", r, nil)
		}, dis)})
	}
	toggle("memory-enabled", "Use memory tools in this project", "Allow agents to query, save, correct, and forget memories and use the automatic features below. Disabling this preserves existing memories, which you can still manage here.", func(s memory.Settings) bool { return !s.Disabled }, func(s *memory.Settings, v bool) { s.Disabled = !v }, nil)
	toggle("memory-user-preferences", "Include user preferences in searches", "Include cross-project preferences saved under User preferences, such as explanation language and personal habits, when searching this project's memory.", func(s memory.Settings) bool { return s.UseUserPreferences }, func(s *memory.Settings, v bool) { s.UseUserPreferences = v }, nil)
	toggle("memory-auto-inject", "Automatically recall relevant memories when sending", "Find relevant memories locally before sending and attach them to the current agent. Search does not call a model; attached content uses conversation context.", func(s memory.Settings) bool { return s.AutoInject }, func(s *memory.Settings, v bool) { s.AutoInject = v }, func() bool { return m.list.Settings.Disabled })
	toggle("memory-auto-extract", "Automatically extract memories after each turn", "Save stable preferences, explicit decisions, and reusable lessons in the background, adding at most one entry per turn. Task progress is excluded and no chat notice is inserted. Edit or forget entries here. Uses the selected agent's quota.", func(s memory.Settings) bool { return s.AutoExtract }, func(s *memory.Settings, v bool) { s.AutoExtract = v }, func() bool {
		return m.list.Settings.Disabled || !m.list.ExtractorAvailable && !m.list.Settings.AutoExtract
	})
	if len(m.list.Extractors) == 0 {
		nodes = append(nodes, ui.FormRow{Title: tr("Extraction agent"), Detail: tr("This background service does not support choosing an extraction agent yet. Upgrade to configure it.")})
	} else {
		var opts []ui.Option[string]
		for _, e := range m.list.Extractors {
			label := model.AgentName(e.Agent)
			if !e.Available {
				label += " · " + tr("Not configured")
			}
			opts = append(opts, ui.Option[string]{Value: e.Agent, Label: label})
		}
		nodes = append(nodes, ui.FormRow{Title: tr("Extraction agent"),
			Detail: tr("Saved per project, independently of the conversation agent. Uses the selected agent's default model."),
			Control: settingsSelect{TestID: "memory-extractor", Label: tr("Extraction agent"), Options: opts, Value: func() string { m.revision.Get(); return m.list.Settings.ExtractionAgent() }, Disabled: disabled, OnChange: func(v string) {
				s := m.list.Settings
				s.ExtractorAgent = v
				r := m.request()
				r.Settings = &s
				m.action("memoryConfigure", r, nil)
			}}})
	}
	if !m.list.ExtractorAvailable {
		nodes = append(nodes, ui.FormRow{Title: tr("Automatically extract memories after each turn"), Detail: tr("No available ") + model.AgentName(m.list.Settings.ExtractionAgent()) + tr(". Automatic extraction is unavailable.")})
	}
	nodes = append(nodes, core.Show{When: Live(func() bool { return m.c.Current().ID != 0 }), Then: func(cx *core.Scope) core.Node {
		return ui.FormRow{Title: tr("Use automatic memory in this conversation"),
			Detail: tr("Follow the project's automatic recall and extraction settings. Disabling this only stops automatic features for this conversation; manual memory tools remain available."),
			Control: settingsToggle("memory-session", tr("Use automatic memory in this conversation"), Live(func() bool { return !m.c.Current().MemoryDisabled }), func(v bool) {
				r := m.request()
				disabled := !v
				r.SessionDisabled = &disabled
				m.action("memorySessionConfigure", r, nil)
			}, disabled)}
	}})
	status := m.list.Extraction
	if status.Pending+status.Completed+status.Failed > 0 {
		message := fmt.Sprintf(tr("Extraction: %d waiting · %d completed · %d skipped or failed · %d saved · %d rejected"), status.Pending, status.Completed, status.Failed, status.Accepted, status.Rejected)
		if status.LastError != "" {
			message += tr("\nLatest extraction issue: ") + tr(status.LastError)
			if status.LastDetail != "" {
				message += " (" + status.LastDetail + ")"
			}
		}
		if !status.PausedUntil.IsZero() {
			message += fmt.Sprintf(tr("\nAutomatic extraction is paused after repeated failures and resumes after %s."), status.PausedUntil.Local().Format("15:04"))
		}
		nodes = append(nodes, ui.FormRow{Title: message})
	}
	footer := ""
	if status.ConsolidationError != "" {
		footer = tr("Latest consolidation issue: ") + tr(status.ConsolidationError)
		if status.ConsolidationDetail != "" {
			footer += " (" + status.ConsolidationDetail + ")"
		}
	} else if !status.ConsolidatedAt.IsZero() {
		footer = fmt.Sprintf(tr("Latest consolidation: %s · %d merged · %d marked as conflicts"),
			status.ConsolidatedAt.Local().Format("01-02 15:04"), status.Merged, status.Conflicts)
	}
	return ui.FormSection{Title: tr("Project settings"), Footer: footer, Children: nodes}
}
func (m *settingsMemory) edit(e memory.Entry) {
	m.editing = true
	m.id, m.version = e.ID, e.Version
	m.kind.Set(firstSettingsText(e.Kind, "fact"))
	m.content.Set(e.Content)
	m.task.Set(e.TaskKey)
	until := ""
	if !e.ValidUntil.IsZero() {
		until = e.ValidUntil.Format(time.RFC3339)
	}
	m.until.Set(until)
	m.changed()
}
func (m *settingsMemory) form(cx *core.Scope) core.Node {
	disabled := func() bool { m.revision.Get(); return m.busy || m.loading || !m.loaded }
	title := tr("Add memory")
	if m.id != "" {
		title = tr("Edit memory")
	}
	save := func() {
		if disabled() || strings.TrimSpace(m.content.Get()) == "" {
			return
		}
		r := m.request()
		r.ID, r.Version, r.Kind, r.Content, r.TaskKey, r.ValidUntil = m.id, m.version, m.kind.Get(), m.content.Get(), m.task.Get(), m.until.Get()
		m.action("memorySave", r, func(json.RawMessage) { m.editing = false })
	}
	return autoCard(cx,
		settingsSectionTitle(title),
		ui.FormRow{Title: tr("Memory type"), Control: settingsSelect{TestID: "memory-kind", Label: tr("Memory type"),
			Options: []ui.Option[string]{{Value: "preference", Label: tr("Preference")}, {Value: "fact", Label: tr("Fact")}, {Value: "progress", Label: tr("Progress")}},
			Value:   m.kind.Get, OnChange: m.kind.Set, Disabled: func() bool { return disabled() || m.scope.Get() == "user" }}},
		settingsField(tr("Memory content"), ui.Input{TestID: "memory-content", Label: tr("Memory content"), Placeholder: tr("Information to retain"), Multiline: true,
			Value: m.content.Get, OnChange: m.content.Set, Disabled: disabled}),
		core.Show{When: func() bool { return m.kind.Get() == "progress" }, Then: func(*core.Scope) core.Node {
			return settingsField(tr("Task identifier"), ui.Input{TestID: "memory-task", Label: tr("Task identifier"),
				Placeholder: tr("Task identifier, for example issue-123"), Value: m.task.Get, OnChange: m.task.Set, Disabled: disabled})
		}},
		settingsField(tr("Expiration"), ui.Input{TestID: "memory-until", Label: tr("Expiration"),
			Value: m.until.Get, OnChange: m.until.Set, Disabled: disabled}),
		settingsText(cx, func() string {
			return tr("Optional RFC3339 time; empty means no expiration. Progress defaults to 7 days.")
		}),
		settingsFormActions(
			settingsButton("memory-cancel", tr("Cancel"), func() { m.editing = false; m.changed() }, disabled),
			ui.Button{TestID: "memory-save", Text: tr("Save memory"), OnPress: save, Disabled: func() bool { return disabled() || strings.TrimSpace(m.content.Get()) == "" }},
		),
	)
}
func (m *settingsMemory) row(cx *core.Scope, e memory.Entry) core.Node {
	r := m.request()
	r.ID, r.Version = e.ID, e.Version
	disabled := func() bool { m.revision.Get(); return m.busy || m.loading || !m.loaded }
	nodes := []core.Node{
		settingsText(cx, func() string {
			return memoryKind(e.Kind) + " · " + memoryState(e.State) + " · " + fmt.Sprintf(tr("Version %d"), e.Version)
		}),
		core.Text{Content: e.Content, Selectable: core.SelectableOn, Style: core.Style{FontSize: 14, LineHeight: 19}},
		settingsText(cx, func() string {
			return tr("As of ") + memoryTime(e.ObservedAt) + tr(" · Expires ") + memoryTime(e.ValidUntil)
		}),
	}
	if e.TaskKey != "" {
		nodes = append(nodes, settingsText(cx, func() string { return tr("Task: ") + e.TaskKey }))
	}
	for _, source := range e.Sources {
		label := map[string]string{"manual": tr("Edited in memory settings"), "user_statement": tr("User statement"), "assistant_report": tr("Assistant report"), "tool_result": tr("Tool result")}[source.EvidenceKind]
		nodes = append(nodes, settingsText(cx, func() string { return label + " · " + memoryTime(source.ObservedAt) + "\n" + source.Quote }))
		if source.Thread != "" {
			nodes = append(nodes, settingsButton("memory-source-"+e.ID, tr("Source conversation"), func() { m.openSource(source.Thread) }, nil))
		}
		if source.Limited {
			nodes = append(nodes, settingsText(cx, func() string {
				return tr("The backend did not provide stable message IDs. Precise navigation is limited.")
			}))
		}
	}
	var actions []core.Node
	if e.State != "forgotten" {
		actions = append(actions, settingsButton("memory-edit-"+e.ID, tr("Edit"), func() { m.edit(e) }, func() bool { return disabled() || e.State == "promoted" }))
		if e.Scope.Kind == "project" && e.Kind == "preference" && e.State == "active" {
			actions = append(actions, settingsButton("memory-promote-user-"+e.ID, tr("Promote to user scope"), func() { m.action("memoryPromoteUser", r, nil) }, disabled))
		}
		if e.Scope.Kind == "project" && e.Kind == "fact" && e.State == "active" {
			actions = append(actions, settingsButton("memory-promote-file-"+e.ID, tr("Promote to project file…"), func() {
				m.id, m.version = e.ID, e.Version
				m.content.Set(e.Content)
				m.file.Set("AGENTS.md")
				m.promotion = &manager.Promotion{}
				m.changed()
			}, disabled))
		}
		actions = append(actions, settingsButton("memory-forget-"+e.ID, tr("Forget"), func() { m.action("memoryForget", r, nil) }, disabled))
	}
	if m.confirmDelete == e.ID {
		actions = append(actions, settingsButton("memory-delete-cancel-"+e.ID, tr("Cancel"), func() { m.confirmDelete = ""; m.changed() }, disabled), ui.Button{TestID: "memory-delete-confirm-" + e.ID, Text: tr("Confirm delete"), Variant: ui.ButtonDanger, OnPress: func() { m.confirmDelete = ""; m.action("memoryDelete", r, nil) }, Disabled: disabled})
	} else {
		actions = append(actions, settingsButton("memory-delete-"+e.ID, tr("Delete"), func() { m.confirmDelete = e.ID; m.changed() }, disabled))
	}
	nodes = append(nodes, settingsActions(actions...))
	if m.promotion != nil && m.id == e.ID {
		nodes = append(nodes, m.promotionView(cx, r))
	}
	return core.View{TestID: "memory-entry-" + e.ID, StyleFunc: nswidgets.CardStyle(cx), Children: nodes}
}
func (m *settingsMemory) promotionView(cx *core.Scope, r manager.Request) core.Node {
	disabled := func() bool { m.revision.Get(); return m.busy || m.loading || !m.loaded }
	req := func() manager.Request { v := r; v.File, v.Content = m.file.Get(), m.content.Get(); return v }
	nodes := []core.Node{settingsText(cx, func() string {
		return tr("Confirm the target and wording before writing to the project file. The original memory will stop being recalled.")
	}), ui.FormRow{Title: tr("Target file"), Control: settingsSelect{TestID: "memory-promotion-file", Label: tr("Target file"), Options: []ui.Option[string]{{Value: "AGENTS.md", Label: "AGENTS.md"}, {Value: "CLAUDE.md", Label: "CLAUDE.md"}}, Value: m.file.Get, OnChange: m.file.Set, Disabled: disabled}}, ui.Input{TestID: "memory-promotion-content", Label: tr("Suggested wording"), Multiline: true, Value: m.content.Get, OnChange: m.content.Set, Disabled: disabled}, settingsActions(settingsButton("memory-promotion-preview", tr("Preview file changes"), func() {
		m.action("memoryPromotionPreview", req(), func(raw json.RawMessage) {
			var p manager.Promotion
			if json.Unmarshal(raw, &p) == nil {
				m.promotion = &p
				m.promotionContent, m.promotionFile = m.content.Get(), m.file.Get()
			}
		})
	}, func() bool { return disabled() || strings.TrimSpace(m.content.Get()) == "" }), settingsButton("memory-promotion-cancel", tr("Cancel promotion"), func() { m.promotion = nil; m.changed() }, disabled))}
	nodes = append(nodes, core.Show{When: func() bool {
		return m.promotion != nil && m.promotion.After != "" && m.content.Get() == m.promotionContent && m.file.Get() == m.promotionFile
	}, Then: func(cx *core.Scope) core.Node {
		p := m.promotion
		return settingsColumn(settingsText(cx, func() string { return tr("Updated ") + p.File + "\n" + p.After }), settingsButton("memory-promotion-confirm", tr("Confirm writing to project file"), func() {
			r := req()
			raw, _ := json.Marshal(p)
			r.Preview = string(raw)
			m.action("memoryPromoteFile", r, func(json.RawMessage) { m.promotion = nil })
		}, disabled))
	}})
	return settingsColumn(nodes...)
}
func (m *settingsMemory) openSource(raw string) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok {
		return
	}
	for _, t := range m.c.AllThreads() {
		if m.c.ThreadRef(t) == ref {
			project := m.c.ThreadWorkspace(t.ID)
			if project != m.c.State().Cwd {
				if err := m.c.ChangeWorkspace(project); err != nil {
					m.err = err.Error()
					m.changed()
					return
				}
			}
			m.c.SelectThread(t)
			if m.onSource != nil {
				m.onSource()
			}
			return
		}
	}
	m.err = tr("Source conversation unavailable. The memory retains its original source identity.")
	m.changed()
}
func memoryTime(t time.Time) string { return memorypresentation.MemoryTime(t) }
func memoryKind(s string) string    { return memorypresentation.MemoryKindLabel(s) }
func memoryState(s string) string   { return memorypresentation.MemoryStateLabel(s) }
