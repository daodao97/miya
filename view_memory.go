package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/model"
)

type memoryUI struct {
	scope, project             string
	list                       manager.List
	loadedAt                   time.Time
	loading, busy              bool
	err                        string
	editing                    bool
	id                         string
	version                    uint64
	kind, content, task, until string
	file                       string
	promotion                  *manager.Promotion
	filter                     string
	confirmDelete              string // entry ID awaiting a second click
}
type memoryNoticeUI struct {
	entry    memory.Entry
	loadedAt time.Time
	loading  bool
	err      string
}

func (app *chatApp) memoryCall(op string, r manager.Request, done func(json.RawMessage, error)) {
	cc := app.c
	finish := func(v any, err error) {
		var raw json.RawMessage
		if err == nil && v != nil {
			raw, _ = json.Marshal(v)
		}
		done(raw, err)
	}
	if cc.remote != nil {
		go func() {
			var raw json.RawMessage
			err := cc.remote.Call(op, r, &raw)
			cc.dispatch(func() { done(raw, err) })
		}()
		return
	}
	v, err := cc.memories().Op(op, r)
	if d, ok := v.(deferred); ok && err == nil {
		go func() { v, err := d(); cc.dispatch(func() { finish(v, err) }) }()
		return
	}
	finish(v, err)
}
func (app *chatApp) loadMemory() {
	m := &app.memories
	if m.loading {
		return
	}
	m.loading = true
	project, scope := app.c.cwd, m.scope
	revision := app.c.memoryRevision
	app.memoryCall("memoryList", manager.Request{Project: project, Scope: scope}, func(raw json.RawMessage, err error) {
		// Don't display a previous project's results after navigation.
		if app.c.cwd != project || m.scope != scope || revision != app.c.memoryRevision {
			m.loading = false
			return
		}
		m.loading = false
		m.loadedAt = time.Now()
		if err != nil {
			m.list = manager.List{}
			m.err = err.Error()
			return
		}
		var next manager.List
		if err = json.Unmarshal(raw, &next); err != nil {
			m.err = err.Error()
			return
		}
		m.list = next
		m.err = ""
	})
}
func (app *chatApp) memoryAction(op string, r manager.Request, done func(json.RawMessage)) {
	m := &app.memories
	if m.busy {
		return
	}
	m.busy = true
	m.err = ""
	app.memoryCall(op, r, func(raw json.RawMessage, err error) {
		m.busy = false
		if err != nil {
			m.err = err.Error()
			m.loadedAt = time.Time{}
			return
		}
		app.memoryNotes = nil // Bodies are ephemeral, never persisted with notices.
		m.loadedAt = time.Time{}
		if done != nil {
			done(raw)
		}
	})
}
func (app *chatApp) memoryView(c *ui.Context) {
	m := &app.memories
	if m.project != app.c.cwd {
		*m = memoryUI{project: app.c.cwd, scope: "project", kind: "fact"}
	}
	rootProject := m.scope != "user" && manager.RootProject(app.c.cwd)
	if !rootProject && !m.loading && time.Since(m.loadedAt) > 5*time.Second {
		app.loadMemory()
	}
	c.After(time.Second)
	ui.Column(c).FillWidth().Gap(20).Children(func() {
		settingsHeader(c, tr("Shared memory"), tr("Saved locally and shared by Codex and Claude. Current instructions and project files take priority. Forgetting removes Miya's content, but not agent history or backups."), func() {
			scopes := []string{"project", "user"}
			i := 0
			if m.scope == "user" {
				i = 1
			}
			before := i
			ui.Segmented(c.Key("scope"), &i, tr("Current project"), tr("User preferences")).Label(tr("Memory scope")).Disabled(m.busy || m.editing)
			if i != before && !m.busy && !m.editing {
				m.scope = scopes[i]
				m.list = manager.List{}
				m.loadedAt = time.Time{}
			}
			if smallButton(c.Key("refresh"), tr("Refresh"), m.loading || m.busy).Clicked() {
				app.loadMemory()
			}
		})
		if rootProject {
			settingsCard(c.Key("no-project")).Children(func() {
				mutedText(c, tr("Open a project first. Project memory and its automatic settings belong to a project folder, not the filesystem root.")).LineHeight(1.5)
			})
			return
		}
		// While editing, the form follows the header instead of the switches.
		if m.scope != "user" && !m.editing {
			app.memorySettings(c)
		}
		if m.err != "" {
			ui.Text(c, m.err).FontSize(12).TextColor(danger).Selectable()
		}
		if m.editing {
			app.memoryForm(c)
			return
		}
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, tr("Memories")).FontSize(12).FontWeight(600).TextColor(muted).PaddingX(4)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.TextInput(c.Key("filter"), &m.filter).Placeholder(tr("Filter content, type, or status")).Label(tr("Filter memories")).Grow(1)
				if ui.PrimaryButton(c.Key("new"), tr("Add memory")).Disabled(m.busy).Clicked() {
					m.editing = true
					m.id = ""
					m.version = 0
					m.content = ""
					m.task = ""
					m.until = ""
					m.kind = "fact"
					if m.scope == "user" {
						m.kind = "preference"
					}
				}
			})
			var visible []memory.Entry
			for _, e := range m.list.Entries {
				if m.filter != "" && !strings.Contains(strings.ToLower(e.Content+" "+e.Kind+" "+e.State), strings.ToLower(m.filter)) {
					continue
				}
				visible = append(visible, e)
			}
			switch {
			case len(m.list.Entries) == 0:
				settingsCard(c.Key("empty")).Children(func() {
					mutedText(c, tr("No memories yet. Add one here or explicitly ask an agent to remember something in a conversation.")).LineHeight(1.5)
				})
			case len(visible) == 0:
				settingsCard(c.Key("empty")).Children(func() { mutedText(c, tr("No matching memories.")) })
			default:
				settingsCard(c.Key("entries")).Padding(0).Gap(0).Clip().Children(func() {
					for i, e := range visible {
						if i > 0 {
							ui.Box(c.Key("sep:" + e.ID)).FillWidth().Height(1).Background(line)
						}
						app.memoryRow(c.Key(e.ID), e)
					}
				})
			}
		})
	})
}

// memorySettings holds the current project's memory switches.
func (app *chatApp) memorySettings(c *ui.Context) {
	m := &app.memories
	busy := m.busy || m.loading
	enabled := !m.list.Settings.Disabled
	toggle := func(key, title, help string, on, disabled bool, set func(bool)) {
		settingRow(c, title, help, func() {
			v := on
			if ui.Switch(c.Key(key), &v).Label(title).Disabled(disabled).Changed() {
				set(v)
			}
		})
	}
	rows := []func(){
		func() {
			toggle("enabled", tr("Use memory tools in this project"), tr("Allow agents to query, save, correct, and forget memories and use the automatic features below. Disabling this preserves existing memories, which you can still manage here."), enabled, busy, func(v bool) {
				settings := m.list.Settings
				settings.Disabled = !v
				app.memoryAction("memoryConfigure", manager.Request{Project: app.c.cwd, Settings: &settings}, nil)
			})
		},
		func() {
			toggle("user-preferences", tr("Include user preferences in searches"), tr("Include cross-project preferences saved under User preferences, such as explanation language and personal habits, when searching this project's memory."), m.list.Settings.UseUserPreferences, busy, func(v bool) {
				settings := m.list.Settings
				settings.UseUserPreferences = v
				app.memoryAction("memoryConfigure", manager.Request{Project: app.c.cwd, Settings: &settings}, nil)
			})
		},
		func() {
			toggle("auto-inject", tr("Automatically recall relevant memories when sending"), tr("Find relevant memories locally before sending and attach them to the current agent. Search does not call a model; attached content uses conversation context."), m.list.Settings.AutoInject, busy || !enabled, func(v bool) {
				settings := m.list.Settings
				settings.AutoInject = v
				app.memoryAction("memoryConfigure", manager.Request{Project: app.c.cwd, Settings: &settings}, nil)
			})
		},
		func() {
			toggle("auto-extract", tr("Automatically extract memories after each turn"), tr("Save stable preferences, explicit decisions, and reusable lessons in the background, adding at most one entry per turn. Task progress is excluded and no chat notice is inserted. Edit or forget entries here. Uses the selected agent's quota."), m.list.Settings.AutoExtract, busy || !enabled || !m.list.ExtractorAvailable && !m.list.Settings.AutoExtract, func(v bool) {
				settings := m.list.Settings
				settings.AutoExtract = v
				app.memoryAction("memoryConfigure", manager.Request{Project: app.c.cwd, Settings: &settings}, nil)
			})
			if len(m.list.Extractors) == 0 {
				mutedText(c, tr("This background service does not support choosing an extraction agent yet. Upgrade to configure it.")).LineHeight(1.5)
			} else {
				ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, tr("Extraction agent")).FontSize(12).TextColor(muted).Margin(0, 4, 0, 0)
					for _, option := range m.list.Extractors {
						agent := option.Agent
						button := quiet(c.Key("extractor:"+agent)).Height(28).PaddingX(10).Gap(6).Border(1, line).Label(tr("Memory extraction agent: ") + backendFor(agent).Name()).Disabled(busy)
						if agent == m.list.Settings.ExtractionAgent() {
							button.Background(chosen)
						}
						button.Children(func() {
							agentIcon(c, agent, 14)
							ui.Text(c, backendFor(agent).Name()).FontSize(12)
							if !option.Available {
								ui.Text(c, tr("Not configured")).FontSize(11).TextColor(muted)
							}
						})
						if button.Clicked() && agent != m.list.Settings.ExtractionAgent() {
							settings := m.list.Settings
							settings.ExtractorAgent = agent
							app.memoryAction("memoryConfigure", manager.Request{Project: app.c.cwd, Settings: &settings}, nil)
						}
					}
				})
				mutedText(c, tr("Saved per project, independently of the conversation agent. Uses the selected agent's default model.")).LineHeight(1.5)
			}
			if !m.list.ExtractorAvailable {
				mutedText(c, tr("No available ")+backendFor(m.list.Settings.ExtractionAgent()).Name()+tr(". Automatic extraction is unavailable.")).LineHeight(1.5)
			}
		},
	}
	if cur := app.c.current(); cur.ID != 0 {
		rows = append(rows, func() {
			toggle("session-memory", tr("Use automatic memory in this conversation"), tr("Follow the project's automatic recall and extraction settings. Disabling this only stops automatic features for this conversation; manual memory tools remain available."), !cur.MemoryDisabled, busy, func(v bool) {
				disabled := !v
				app.memoryAction("memorySessionConfigure", manager.Request{Project: app.c.cwd, Thread: cur.ID, SessionDisabled: &disabled}, nil)
			})
		})
	}
	footnote := ""
	if status := m.list.Extraction; status.Pending+status.Completed+status.Failed > 0 {
		footnote = fmt.Sprintf(tr("Extraction: %d waiting · %d completed · %d skipped or failed · %d saved · %d rejected"), status.Pending, status.Completed, status.Failed, status.Accepted, status.Rejected)
		if status.LastError != "" {
			footnote += tr("\nLatest extraction issue: ") + tr(status.LastError)
			if status.LastDetail != "" {
				footnote += " (" + status.LastDetail + ")"
			}
		}
		if !status.PausedUntil.IsZero() {
			footnote += fmt.Sprintf(tr("\nAutomatic extraction is paused after repeated failures and resumes after %s."), status.PausedUntil.Local().Format("15:04"))
		}
	}
	settingsGroup(c, tr("Project settings"), footnote, rows...)
}

func memoryKindLabel(kind string) string {
	switch kind {
	case "preference":
		return tr("Preference")
	case "fact":
		return tr("Fact")
	case "progress":
		return tr("Progress")
	}
	return kind
}
func memoryStateLabel(state string) string {
	switch state {
	case "active":
		return tr("Active")
	case "expired":
		return tr("Expired")
	case "forgotten":
		return tr("Forgotten")
	case "promoted":
		return tr("Promoted")
	case "superseded":
		return tr("Updated")
	case "conflicted":
		return tr("Conflict · Needs verification")
	}
	return state
}
func memoryTime(t time.Time) string {
	if t.IsZero() {
		return tr("Not set")
	}
	return t.Local().Format("2006-01-02 15:04")
}
func (app *chatApp) memoryRow(c *ui.Context, e memory.Entry) {
	m := &app.memories
	ui.Column(c).FillWidth().MinWidth(0).Padding(14, 16).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(6).Children(func() {
			if e.Kind != "" {
				badge(c, memoryKindLabel(e.Kind))
			}
			badge(c, memoryStateLabel(e.State))
			ui.Text(c, fmt.Sprintf(tr("Version %d"), e.Version)).FontSize(11).TextColor(muted)
		})
		if e.Content != "" {
			ui.Text(c, e.Content).FontSize(13).LineHeight(1.5).Selectable()
		}
		ui.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
			ui.Text(c, tr("As of ")+memoryTime(e.ObservedAt)+tr(" · Expires ")+memoryTime(e.ValidUntil)).FontSize(11).TextColor(muted)
			if e.TaskKey != "" {
				ui.Text(c, tr("Task: ")+e.TaskKey).FontSize(11).TextColor(muted)
			}
		})
		for i, src := range e.Sources {
			app.memorySourceView(c.Key(i), src)
		}
		req := manager.Request{Project: app.c.cwd, Scope: m.scope, ID: e.ID, Version: e.Version, Thread: app.c.active()}
		// Delete removes the record from this list. Miya keeps the tombstone, so
		// the old content still cannot be extracted again. Two clicks are needed.
		deleteButton := func() {
			if m.confirmDelete == e.ID {
				if smallButton(c.Key("delete-confirm"), tr("Confirm delete"), m.busy).TextColor(danger).Clicked() {
					m.confirmDelete = ""
					app.memoryAction("memoryDelete", req, nil)
				}
				if smallButton(c.Key("delete-cancel"), tr("Cancel"), m.busy).Clicked() {
					m.confirmDelete = ""
				}
				return
			}
			if smallButton(c.Key("delete"), tr("Delete"), m.busy).TextColor(danger).Clicked() {
				m.confirmDelete = e.ID
			}
		}
		if e.State == "forgotten" {
			ui.Row(c).Wrap().Gap(6).Children(deleteButton)
			return
		}
		ui.Row(c).Wrap().Gap(6).Children(func() {
			if smallButton(c.Key("edit"), tr("Edit"), m.busy || e.State == "promoted").Clicked() {
				m.editing = true
				m.id = e.ID
				m.version = e.Version
				m.kind = e.Kind
				m.content = e.Content
				m.task = e.TaskKey
				m.until = ""
				if !e.ValidUntil.IsZero() {
					m.until = e.ValidUntil.Format(time.RFC3339)
				}
			}
			if e.Scope.Kind == "project" && e.Kind == "preference" && e.State == "active" {
				if smallButton(c.Key("user"), tr("Promote to user scope"), m.busy).Clicked() {
					app.memoryAction("memoryPromoteUser", req, nil)
				}
			}
			if e.Scope.Kind == "project" && e.Kind == "fact" && e.State == "active" {
				if smallButton(c.Key("promote"), tr("Promote to project file…"), m.busy).Clicked() {
					m.id = e.ID
					m.version = e.Version
					m.content = e.Content
					m.kind = e.Kind
					m.file = "AGENTS.md"
					m.promotion = &manager.Promotion{}
				}
			}
			if smallButton(c.Key("forget"), tr("Forget"), m.busy).TextColor(danger).Clicked() {
				app.memoryAction("memoryForget", req, nil)
			}
			deleteButton()
		})
		if m.promotion != nil && m.id == e.ID {
			app.memoryPromotionView(c, e)
		}
	})
}
func (app *chatApp) memorySourceView(c *ui.Context, src memory.Source) {
	label := map[string]string{"manual": tr("Edited in memory settings"), "user_statement": tr("User statement"), "assistant_report": tr("Assistant report"), "tool_result": tr("Tool result")}[src.EvidenceKind]
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, label+" · "+memoryTime(src.ObservedAt)).FontSize(11).TextColor(muted)
			if src.Thread != "" {
				if smallButton(c.Key("source"), tr("Source conversation"), false).Height(24).Clicked() {
					app.openMemorySource(src.Thread)
				}
				if src.Limited {
					ui.Text(c, tr("The backend did not provide stable message IDs. Precise navigation is limited.")).FontSize(11).TextColor(muted)
				}
			}
		})
		if src.Quote != "" {
			ui.Text(c, src.Quote).FontSize(12).TextColor(muted).LineHeight(1.45).Selectable().FillWidth().Padding(8, 10).Background(bubble).Radius(6)
		}
	})
}
func (app *chatApp) openMemorySource(raw string) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok {
		return
	}
	for _, t := range app.c.allThreads() {
		if app.c.threadRef(t) == ref {
			project := app.c.threadWorkspace(t.ID)
			if project != app.c.cwd {
				if err := app.c.changeWorkspace(project); err != nil {
					app.notice = err.Error()
					return
				}
			}
			app.section = ""
			app.c.selectThread(t)
			return
		}
	}
	app.notice = tr("Source conversation unavailable. The memory retains its original source identity.")
}
func (app *chatApp) memoryForm(c *ui.Context) {
	m := &app.memories
	settingsCard(c).Padding(20).Gap(14).Children(func() {
		title := tr("Add memory")
		if m.id != "" {
			title = tr("Edit memory")
		}
		ui.Text(c, title).FontSize(14).FontWeight(600)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, tr("Type")).FontSize(12).TextColor(muted)
			if m.scope == "user" {
				badge(c, memoryKindLabel(m.kind))
				return
			}
			kinds := []string{"preference", "fact", "progress"}
			labels := make([]string, len(kinds))
			i := 0
			for j, kind := range kinds {
				labels[j] = memoryKindLabel(kind)
				if kind == m.kind {
					i = j
				}
			}
			before := i
			ui.Segmented(c.Key("kind"), &i, labels...).Label(tr("Memory type")).Disabled(m.busy)
			if i != before && !m.busy {
				m.kind = kinds[i]
			}
		})
		ui.TextArea(c.Key("content"), &m.content).Lines(3, 10).FillWidth().Label(tr("Memory content")).Placeholder(tr("Information to retain"))
		if m.kind == "progress" {
			ui.TextInput(c.Key("task"), &m.task).FillWidth().Label(tr("Task identifier")).Placeholder(tr("Task identifier, for example issue-123"))
		}
		ui.TextInput(c.Key("until"), &m.until).FillWidth().Label(tr("Expiration")).Placeholder(tr("Optional RFC3339 time; empty means no expiration. Progress defaults to 7 days."))
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("cancel"), tr("Cancel")).Disabled(m.busy).Clicked() {
				m.editing = false
			}
			if ui.PrimaryButton(c.Key("save"), tr("Save memory")).Disabled(m.busy || strings.TrimSpace(m.content) == "").Clicked() {
				app.memoryAction("memorySave", manager.Request{Project: app.c.cwd, Scope: m.scope, ID: m.id, Version: m.version, Kind: m.kind, Content: m.content, TaskKey: m.task, ValidUntil: m.until, Thread: app.c.active()}, func(json.RawMessage) { m.editing = false })
			}
		})
	})
}
func (app *chatApp) memoryPromotionView(c *ui.Context, e memory.Entry) {
	m := &app.memories
	ui.Column(c).FillWidth().Padding(12).Gap(10).Background(bubble).Radius(8).Children(func() {
		mutedText(c, tr("Confirm the target and wording before writing to the project file. The original memory will stop being recalled."))
		files := []string{"AGENTS.md", "CLAUDE.md"}
		i := 0
		if m.file == "CLAUDE.md" {
			i = 1
		}
		before := i
		ui.Segmented(c.Key("file"), &i, files...).Label(tr("Target file")).Disabled(m.busy).AlignSelf(ui.Start)
		if i != before && !m.busy {
			m.file = files[i]
			m.promotion = &manager.Promotion{}
		}
		if ui.TextArea(c.Key("wording"), &m.content).Lines(2, 6).FillWidth().Label(tr("Suggested wording")).Changed() {
			m.promotion = &manager.Promotion{}
		}
		req := manager.Request{Project: app.c.cwd, ID: e.ID, Version: e.Version, File: m.file, Content: m.content, Thread: app.c.active()}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			if smallButton(c.Key("preview"), tr("Preview file changes"), m.busy || strings.TrimSpace(m.content) == "").Clicked() {
				app.memoryAction("memoryPromotionPreview", req, func(raw json.RawMessage) {
					var p manager.Promotion
					if json.Unmarshal(raw, &p) == nil {
						m.promotion = &p
					}
				})
			}
			if smallButton(c.Key("cancel"), tr("Cancel promotion"), m.busy).Clicked() {
				m.promotion = nil
			}
		})
		if m.promotion != nil && m.promotion.After != "" {
			ui.Text(c, tr("Updated ")+m.promotion.File).FontSize(12).FontWeight(600)
			ui.Scroll(c.Key("preview-content")).MaxHeight(240).FillWidth().Background(panel).Border(1, line).Radius(8).Children(func() {
				ui.Text(c, m.promotion.After).FontSize(12).Font(monoFont).LineHeight(1.5).Selectable().Padding(10, 12)
			})
			if ui.PrimaryButton(c.Key("confirm"), tr("Confirm writing to project file")).Disabled(m.busy).AlignSelf(ui.Start).Clicked() {
				b, _ := json.Marshal(m.promotion)
				req.Preview = string(b)
				app.memoryAction("memoryPromoteFile", req, func(json.RawMessage) { m.promotion = nil })
			}
		}
	})
}
func (app *chatApp) memoryNoticeView(c *ui.Context, n *MemoryNotice) {
	if n == nil || !n.Explicit {
		return
	}
	if app.mobile {
		ui.Text(c, tr("Miya memory changed. View it on the memory page; edit on your desktop.")).FontSize(12).TextColor(muted)
		return
	}
	if app.memoryNotes == nil {
		app.memoryNotes = map[string]*memoryNoticeUI{}
	}
	key := fmt.Sprintf("%s/%s/%s/%d", n.Project, n.Scope, n.ID, n.Version)
	state := app.memoryNotes[key]
	if state == nil {
		state = &memoryNoticeUI{}
		app.memoryNotes[key] = state
	}
	if !state.loading && time.Since(state.loadedAt) > 3*time.Second {
		state.loading = true
		app.memoryCall("memoryGet", manager.Request{Project: n.Project, Scope: n.Scope, ID: n.ID, Version: n.Version}, func(raw json.RawMessage, err error) {
			state.loading = false
			state.loadedAt = time.Now()
			state.entry = memory.Entry{}
			if err != nil {
				state.err = err.Error()
				return
			}
			state.err = ""
			_ = json.Unmarshal(raw, &state.entry)
		})
	}
	c.After(3 * time.Second)
	ui.Column(c).FillWidth().Padding(12).Gap(6).Background(bubble).Radius(10).Children(func() {
		scope := tr("Current project")
		if n.Scope == "user" {
			scope = tr("User preferences (across projects)")
		}
		ui.Text(c, tr("Miya memory · ")+scope).FontSize(12).FontWeight(600)
		if state.err != "" {
			ui.Text(c, state.err).FontSize(12).TextColor(muted)
			return
		}
		e := state.entry
		if e.ID == "" {
			ui.Text(c, tr("Loading memory…")).FontSize(12).TextColor(muted)
			return
		}
		if e.Content != "" {
			ui.Text(c, e.Content).FontSize(12).LineHeight(1.4).Selectable()
		}
		if e.State != "active" && e.State != "expired" && e.State != "conflicted" {
			ui.Text(c, memoryStateLabel(e.State)).FontSize(12).TextColor(muted)
			return
		}
		if e.State == "conflicted" {
			ui.Text(c, memoryStateLabel(e.State)).FontSize(12).TextColor(muted)
		}
		if ui.Button(c.Key("undo"), tr("Undo memory change")).Disabled(app.memories.busy).Clicked() {
			app.memoryAction("memoryForget", manager.Request{Project: n.Project, Scope: n.Scope, ID: n.ID, Version: n.Version}, nil)
		}
	})
}
