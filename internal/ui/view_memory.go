package desktopui

import (
	json "encoding/json"
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	memory "miya/internal/memory"
	manager "miya/internal/memory/manager"
	model "miya/internal/model"
	widgets "miya/internal/widgets"
	strings "strings"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

type MemoryUI struct {
	Scope, Project             string
	List                       manager.List
	LoadedAt                   time.Time
	Loading, Busy              bool
	Err                        string
	editing                    bool
	Id                         string
	Version                    uint64
	kind, Content, task, until string
	File                       string
	Promotion                  *manager.Promotion
	filter                     string
	confirmDelete              string // entry ID awaiting a second click
}

type MemoryNoticeUI struct {
	Entry    memory.Entry
	LoadedAt time.Time
	loading  bool
	err      string
}

func (app *App) memoryCall(op string, r manager.Request, done func(json.RawMessage, error)) {
	cc := app.C
	finish := func(v any, err error) {
		var raw json.RawMessage
		if err == nil && v != nil {
			raw, _ = json.Marshal(v)
		}
		done(raw, err)
	}
	if cc.State().Remote != nil {
		go func() {
			var raw json.RawMessage
			err := cc.State().Remote.Call(op, r, &raw)
			cc.Dispatch(func() { done(raw, err) })
		}()
		return
	}
	v, err := cc.Memories().Op(op, r)
	if d, ok := v.(appmodel.Deferred); ok && err == nil {
		go func() { v, err := d(); cc.Dispatch(func() { finish(v, err) }) }()
		return
	}
	finish(v, err)
}

func (app *App) LoadMemory() {
	m := &app.Memories
	if m.Loading {
		return
	}
	m.Loading = true
	project, scope := app.C.State().Cwd, m.Scope
	revision := app.C.State().MemoryRevision
	app.memoryCall("memoryList", manager.Request{Project: project, Scope: scope}, func(raw json.RawMessage, err error) {
		// Don't display a previous project's results after navigation.
		if app.C.State().Cwd != project || m.Scope != scope || revision != app.C.State().MemoryRevision {
			m.Loading = false
			return
		}
		m.Loading = false
		m.LoadedAt = time.Now()
		if err != nil {
			m.List = manager.List{}
			m.Err = err.Error()
			return
		}
		var next manager.List
		if err = json.Unmarshal(raw, &next); err != nil {
			m.Err = err.Error()
			return
		}
		m.List = next
		m.Err = ""
	})
}

func (app *App) memoryAction(op string, r manager.Request, done func(json.RawMessage)) {
	m := &app.Memories
	if m.Busy {
		return
	}
	m.Busy = true
	m.Err = ""
	app.memoryCall(op, r, func(raw json.RawMessage, err error) {
		m.Busy = false
		if err != nil {
			m.Err = err.Error()
			m.LoadedAt = time.Time{}
			return
		}
		app.MemoryNotes = nil // Bodies are ephemeral, never persisted with notices.
		m.LoadedAt = time.Time{}
		if done != nil {
			done(raw)
		}
	})
}

func (app *App) memoryView(c *ui.Context) {
	m := &app.Memories
	if m.Project != app.C.State().Cwd {
		*m = MemoryUI{Project: app.C.State().Cwd, Scope: "project", kind: "fact"}
	}
	rootProject := m.Scope != "user" && manager.RootProject(app.C.State().Cwd)
	if !rootProject && !m.Loading && time.Since(m.LoadedAt) > 5*time.Second {
		app.LoadMemory()
	}
	c.After(time.Second)
	ui.Column(c).FillWidth().Gap(20).Children(func() {
		widgets.SettingsHeader(c, tr("Shared memory"), tr("Saved locally and shared by Codex and Claude. Current instructions and project files take priority. Forgetting removes Miya's content, but not agent history or backups."), func() {
			scopes := []string{"project", "user"}
			i := 0
			if m.Scope == "user" {
				i = 1
			}
			before := i
			ui.Segmented(c.Key("scope"), &i, tr("Current project"), tr("User preferences")).Label(tr("Memory scope")).Disabled(m.Busy || m.editing)
			if i != before && !m.Busy && !m.editing {
				m.Scope = scopes[i]
				m.List = manager.List{}
				m.LoadedAt = time.Time{}
			}
			if widgets.SmallButton(c.Key("refresh"), tr("Refresh"), m.Loading || m.Busy).Clicked() {
				app.LoadMemory()
			}
		})
		if rootProject {
			widgets.SettingsCard(c.Key("no-project")).Children(func() {
				widgets.MutedText(c, tr("Open a project first. Project memory and its automatic settings belong to a project folder, not the filesystem root.")).LineHeight(1.5)
			})
			return
		}
		// While editing, the form follows the header instead of the switches.
		if m.Scope != "user" && !m.editing {
			app.memorySettings(c)
		}
		if m.Err != "" {
			ui.Text(c, m.Err).FontSize(12).TextColor(widgets.Danger).Selectable()
		}
		if m.editing {
			app.memoryForm(c)
			return
		}
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, tr("Memories")).FontSize(12).FontWeight(600).TextColor(widgets.Muted).PaddingX(4)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.TextInput(c.Key("filter"), &m.filter).Placeholder(tr("Filter content, type, or status")).Label(tr("Filter memories")).Grow(1)
				if ui.PrimaryButton(c.Key("new"), tr("Add memory")).Disabled(m.Busy).Clicked() {
					m.editing = true
					m.Id = ""
					m.Version = 0
					m.Content = ""
					m.task = ""
					m.until = ""
					m.kind = "fact"
					if m.Scope == "user" {
						m.kind = "preference"
					}
				}
			})
			var visible []memory.Entry
			for _, e := range m.List.Entries {
				if m.filter != "" && !strings.Contains(strings.ToLower(e.Content+" "+e.Kind+" "+e.State), strings.ToLower(m.filter)) {
					continue
				}
				visible = append(visible, e)
			}
			switch {
			case len(m.List.Entries) == 0:
				widgets.SettingsCard(c.Key("empty")).Children(func() {
					widgets.MutedText(c, tr("No memories yet. Add one here or explicitly ask an agent to remember something in a conversation.")).LineHeight(1.5)
				})
			case len(visible) == 0:
				widgets.SettingsCard(c.Key("empty")).Children(func() { widgets.MutedText(c, tr("No matching memories.")) })
			default:
				widgets.SettingsCard(c.Key("entries")).Padding(0).Gap(0).Clip().Children(func() {
					for i, e := range visible {
						if i > 0 {
							ui.Box(c.Key("sep:" + e.ID)).FillWidth().Height(1).Background(widgets.Line)
						}
						app.memoryRow(c.Key(e.ID), e)
					}
				})
			}
		})
	})
}

// memorySettings holds the current project's memory switches.
func (app *App) memorySettings(c *ui.Context) {
	m := &app.Memories
	busy := m.Busy || m.Loading
	enabled := !m.List.Settings.Disabled
	toggle := func(key, title, help string, on, disabled bool, set func(bool)) {
		widgets.SettingRow(c, title, help, func() {
			v := on
			if ui.Switch(c.Key(key), &v).Label(title).Disabled(disabled).Changed() {
				set(v)
			}
		})
	}
	rows := []func(){
		func() {
			toggle("enabled", tr("Use memory tools in this project"), tr("Allow agents to query, save, correct, and forget memories and use the automatic features below. Disabling this preserves existing memories, which you can still manage here."), enabled, busy, func(v bool) {
				settings := m.List.Settings
				settings.Disabled = !v
				app.memoryAction("memoryConfigure", manager.Request{Project: app.C.State().Cwd, Settings: &settings}, nil)
			})
		},
		func() {
			toggle("user-preferences", tr("Include user preferences in searches"), tr("Include cross-project preferences saved under User preferences, such as explanation language and personal habits, when searching this project's memory."), m.List.Settings.UseUserPreferences, busy, func(v bool) {
				settings := m.List.Settings
				settings.UseUserPreferences = v
				app.memoryAction("memoryConfigure", manager.Request{Project: app.C.State().Cwd, Settings: &settings}, nil)
			})
		},
		func() {
			toggle("auto-inject", tr("Automatically recall relevant memories when sending"), tr("Find relevant memories locally before sending and attach them to the current agent. Search does not call a model; attached content uses conversation context."), m.List.Settings.AutoInject, busy || !enabled, func(v bool) {
				settings := m.List.Settings
				settings.AutoInject = v
				app.memoryAction("memoryConfigure", manager.Request{Project: app.C.State().Cwd, Settings: &settings}, nil)
			})
		},
		func() {
			toggle("auto-extract", tr("Automatically extract memories after each turn"), tr("Save stable preferences, explicit decisions, and reusable lessons in the background, adding at most one entry per turn. Task progress is excluded and no chat notice is inserted. Edit or forget entries here. Uses the selected agent's quota."), m.List.Settings.AutoExtract, busy || !enabled || !m.List.ExtractorAvailable && !m.List.Settings.AutoExtract, func(v bool) {
				settings := m.List.Settings
				settings.AutoExtract = v
				app.memoryAction("memoryConfigure", manager.Request{Project: app.C.State().Cwd, Settings: &settings}, nil)
			})
			if len(m.List.Extractors) == 0 {
				widgets.MutedText(c, tr("This background service does not support choosing an extraction agent yet. Upgrade to configure it.")).LineHeight(1.5)
			} else {
				ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, tr("Extraction agent")).FontSize(12).TextColor(widgets.Muted).Margin(0, 4, 0, 0)
					for _, option := range m.List.Extractors {
						agent := option.Agent
						button := widgets.Quiet(c.Key("extractor:"+agent)).Height(28).PaddingX(10).Gap(6).Border(1, widgets.Line).Label(tr("Memory extraction agent: ") + model.AgentName(agent)).Disabled(busy)
						if agent == m.List.Settings.ExtractionAgent() {
							button.Background(widgets.Chosen)
						}
						button.Children(func() {
							widgets.AgentIcon(c, agent, 14)
							ui.Text(c, model.AgentName(agent)).FontSize(12)
							if !option.Available {
								ui.Text(c, tr("Not configured")).FontSize(11).TextColor(widgets.Muted)
							}
						})
						if button.Clicked() && agent != m.List.Settings.ExtractionAgent() {
							settings := m.List.Settings
							settings.ExtractorAgent = agent
							app.memoryAction("memoryConfigure", manager.Request{Project: app.C.State().Cwd, Settings: &settings}, nil)
						}
					}
				})
				widgets.MutedText(c, tr("Saved per project, independently of the conversation agent. Uses the selected agent's default model.")).LineHeight(1.5)
			}
			if !m.List.ExtractorAvailable {
				widgets.MutedText(c, tr("No available ")+model.AgentName(m.List.Settings.ExtractionAgent())+tr(". Automatic extraction is unavailable.")).LineHeight(1.5)
			}
		},
	}
	if cur := app.C.Current(); cur.ID != 0 {
		rows = append(rows, func() {
			toggle("session-memory", tr("Use automatic memory in this conversation"), tr("Follow the project's automatic recall and extraction settings. Disabling this only stops automatic features for this conversation; manual memory tools remain available."), !cur.MemoryDisabled, busy, func(v bool) {
				disabled := !v
				app.memoryAction("memorySessionConfigure", manager.Request{Project: app.C.State().Cwd, Thread: cur.ID, SessionDisabled: &disabled}, nil)
			})
		})
	}
	footnote := ""
	if status := m.List.Extraction; status.Pending+status.Completed+status.Failed > 0 {
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
	widgets.SettingsGroup(c, tr("Project settings"), footnote, rows...)
}

func (app *App) memoryRow(c *ui.Context, e memory.Entry) {
	m := &app.Memories
	ui.Column(c).FillWidth().MinWidth(0).Padding(14, 16).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(6).Children(func() {
			if e.Kind != "" {
				badge(c, widgets.MemoryKindLabel(e.Kind))
			}
			badge(c, widgets.MemoryStateLabel(e.State))
			ui.Text(c, fmt.Sprintf(tr("Version %d"), e.Version)).FontSize(11).TextColor(widgets.Muted)
		})
		if e.Content != "" {
			ui.Text(c, e.Content).FontSize(13).LineHeight(1.5).Selectable()
		}
		ui.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
			ui.Text(c, tr("As of ")+widgets.MemoryTime(e.ObservedAt)+tr(" · Expires ")+widgets.MemoryTime(e.ValidUntil)).FontSize(11).TextColor(widgets.Muted)
			if e.TaskKey != "" {
				ui.Text(c, tr("Task: ")+e.TaskKey).FontSize(11).TextColor(widgets.Muted)
			}
		})
		for i, src := range e.Sources {
			app.memorySourceView(c.Key(i), src)
		}
		req := manager.Request{Project: app.C.State().Cwd, Scope: m.Scope, ID: e.ID, Version: e.Version, Thread: app.C.State().Active()}
		// Delete removes the record from this list. Miya keeps the tombstone, so
		// the old content still cannot be extracted again. Two clicks are needed.
		deleteButton := func() {
			if m.confirmDelete == e.ID {
				if widgets.SmallButton(c.Key("delete-confirm"), tr("Confirm delete"), m.Busy).TextColor(widgets.Danger).Clicked() {
					m.confirmDelete = ""
					app.memoryAction("memoryDelete", req, nil)
				}
				if widgets.SmallButton(c.Key("delete-cancel"), tr("Cancel"), m.Busy).Clicked() {
					m.confirmDelete = ""
				}
				return
			}
			if widgets.SmallButton(c.Key("delete"), tr("Delete"), m.Busy).TextColor(widgets.Danger).Clicked() {
				m.confirmDelete = e.ID
			}
		}
		if e.State == "forgotten" {
			ui.Row(c).Wrap().Gap(6).Children(deleteButton)
			return
		}
		ui.Row(c).Wrap().Gap(6).Children(func() {
			if widgets.SmallButton(c.Key("edit"), tr("Edit"), m.Busy || e.State == "promoted").Clicked() {
				m.editing = true
				m.Id = e.ID
				m.Version = e.Version
				m.kind = e.Kind
				m.Content = e.Content
				m.task = e.TaskKey
				m.until = ""
				if !e.ValidUntil.IsZero() {
					m.until = e.ValidUntil.Format(time.RFC3339)
				}
			}
			if e.Scope.Kind == "project" && e.Kind == "preference" && e.State == "active" {
				if widgets.SmallButton(c.Key("user"), tr("Promote to user scope"), m.Busy).Clicked() {
					app.memoryAction("memoryPromoteUser", req, nil)
				}
			}
			if e.Scope.Kind == "project" && e.Kind == "fact" && e.State == "active" {
				if widgets.SmallButton(c.Key("promote"), tr("Promote to project file…"), m.Busy).Clicked() {
					m.Id = e.ID
					m.Version = e.Version
					m.Content = e.Content
					m.kind = e.Kind
					m.File = "AGENTS.md"
					m.Promotion = &manager.Promotion{}
				}
			}
			if widgets.SmallButton(c.Key("forget"), tr("Forget"), m.Busy).TextColor(widgets.Danger).Clicked() {
				app.memoryAction("memoryForget", req, nil)
			}
			deleteButton()
		})
		if m.Promotion != nil && m.Id == e.ID {
			app.memoryPromotionView(c, e)
		}
	})
}

func (app *App) memorySourceView(c *ui.Context, src memory.Source) {
	label := map[string]string{"manual": tr("Edited in memory settings"), "user_statement": tr("User statement"), "assistant_report": tr("Assistant report"), "tool_result": tr("Tool result")}[src.EvidenceKind]
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, label+" · "+widgets.MemoryTime(src.ObservedAt)).FontSize(11).TextColor(widgets.Muted)
			if src.Thread != "" {
				if widgets.SmallButton(c.Key("source"), tr("Source conversation"), false).Height(24).Clicked() {
					app.openMemorySource(src.Thread)
				}
				if src.Limited {
					ui.Text(c, tr("The backend did not provide stable message IDs. Precise navigation is limited.")).FontSize(11).TextColor(widgets.Muted)
				}
			}
		})
		if src.Quote != "" {
			ui.Text(c, src.Quote).FontSize(12).TextColor(widgets.Muted).LineHeight(1.45).Selectable().FillWidth().Padding(8, 10).Background(widgets.Bubble).Radius(6)
		}
	})
}

func (app *App) openMemorySource(raw string) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok {
		return
	}
	for _, t := range app.C.AllThreads() {
		if app.C.ThreadRef(t) == ref {
			project := app.C.ThreadWorkspace(t.ID)
			if project != app.C.State().Cwd {
				if err := app.C.ChangeWorkspace(project); err != nil {
					app.Notice = err.Error()
					return
				}
			}
			app.Section = ""
			app.C.SelectThread(t)
			return
		}
	}
	app.Notice = tr("Source conversation unavailable. The memory retains its original source identity.")
}

func (app *App) memoryForm(c *ui.Context) {
	m := &app.Memories
	widgets.SettingsCard(c).Padding(20).Gap(14).Children(func() {
		title := tr("Add memory")
		if m.Id != "" {
			title = tr("Edit memory")
		}
		ui.Text(c, title).FontSize(14).FontWeight(600)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, tr("Type")).FontSize(12).TextColor(widgets.Muted)
			if m.Scope == "user" {
				badge(c, widgets.MemoryKindLabel(m.kind))
				return
			}
			kinds := []string{"preference", "fact", "progress"}
			labels := make([]string, len(kinds))
			i := 0
			for j, kind := range kinds {
				labels[j] = widgets.MemoryKindLabel(kind)
				if kind == m.kind {
					i = j
				}
			}
			before := i
			ui.Segmented(c.Key("kind"), &i, labels...).Label(tr("Memory type")).Disabled(m.Busy)
			if i != before && !m.Busy {
				m.kind = kinds[i]
			}
		})
		ui.TextArea(c.Key("content"), &m.Content).Lines(3, 10).FillWidth().Label(tr("Memory content")).Placeholder(tr("Information to retain"))
		if m.kind == "progress" {
			ui.TextInput(c.Key("task"), &m.task).FillWidth().Label(tr("Task identifier")).Placeholder(tr("Task identifier, for example issue-123"))
		}
		ui.TextInput(c.Key("until"), &m.until).FillWidth().Label(tr("Expiration")).Placeholder(tr("Optional RFC3339 time; empty means no expiration. Progress defaults to 7 days."))
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("cancel"), tr("Cancel")).Disabled(m.Busy).Clicked() {
				m.editing = false
			}
			if ui.PrimaryButton(c.Key("save"), tr("Save memory")).Disabled(m.Busy || strings.TrimSpace(m.Content) == "").Clicked() {
				app.memoryAction("memorySave", manager.Request{Project: app.C.State().Cwd, Scope: m.Scope, ID: m.Id, Version: m.Version, Kind: m.kind, Content: m.Content, TaskKey: m.task, ValidUntil: m.until, Thread: app.C.State().Active()}, func(json.RawMessage) { m.editing = false })
			}
		})
	})
}

func (app *App) memoryPromotionView(c *ui.Context, e memory.Entry) {
	m := &app.Memories
	ui.Column(c).FillWidth().Padding(12).Gap(10).Background(widgets.Bubble).Radius(8).Children(func() {
		widgets.MutedText(c, tr("Confirm the target and wording before writing to the project file. The original memory will stop being recalled."))
		files := []string{"AGENTS.md", "CLAUDE.md"}
		i := 0
		if m.File == "CLAUDE.md" {
			i = 1
		}
		before := i
		ui.Segmented(c.Key("file"), &i, files...).Label(tr("Target file")).Disabled(m.Busy).AlignSelf(ui.Start)
		if i != before && !m.Busy {
			m.File = files[i]
			m.Promotion = &manager.Promotion{}
		}
		if ui.TextArea(c.Key("wording"), &m.Content).Lines(2, 6).FillWidth().Label(tr("Suggested wording")).Changed() {
			m.Promotion = &manager.Promotion{}
		}
		req := manager.Request{Project: app.C.State().Cwd, ID: e.ID, Version: e.Version, File: m.File, Content: m.Content, Thread: app.C.State().Active()}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			if widgets.SmallButton(c.Key("preview"), tr("Preview file changes"), m.Busy || strings.TrimSpace(m.Content) == "").Clicked() {
				app.memoryAction("memoryPromotionPreview", req, func(raw json.RawMessage) {
					var p manager.Promotion
					if json.Unmarshal(raw, &p) == nil {
						m.Promotion = &p
					}
				})
			}
			if widgets.SmallButton(c.Key("cancel"), tr("Cancel promotion"), m.Busy).Clicked() {
				m.Promotion = nil
			}
		})
		if m.Promotion != nil && m.Promotion.After != "" {
			ui.Text(c, tr("Updated ")+m.Promotion.File).FontSize(12).FontWeight(600)
			ui.Scroll(c.Key("preview-content")).MaxHeight(240).FillWidth().Background(widgets.Panel).Border(1, widgets.Line).Radius(8).Children(func() {
				ui.Text(c, m.Promotion.After).FontSize(12).Font(widgets.MonoFont).LineHeight(1.5).Selectable().Padding(10, 12)
			})
			if ui.PrimaryButton(c.Key("confirm"), tr("Confirm writing to project file")).Disabled(m.Busy).AlignSelf(ui.Start).Clicked() {
				b, _ := json.Marshal(m.Promotion)
				req.Preview = string(b)
				app.memoryAction("memoryPromoteFile", req, func(json.RawMessage) { m.Promotion = nil })
			}
		}
	})
}

func (app *App) memoryNoticeView(c *ui.Context, n *appmodel.MemoryNotice) {
	if n == nil || !n.Explicit {
		return
	}
	if app.Mobile {
		ui.Text(c, tr("Miya memory changed. View it on the memory page; edit on your desktop.")).FontSize(12).TextColor(widgets.Muted)
		return
	}
	if app.MemoryNotes == nil {
		app.MemoryNotes = map[string]*MemoryNoticeUI{}
	}
	key := fmt.Sprintf("%s/%s/%s/%d", n.Project, n.Scope, n.ID, n.Version)
	state := app.MemoryNotes[key]
	if state == nil {
		state = &MemoryNoticeUI{}
		app.MemoryNotes[key] = state
	}
	if !state.loading && time.Since(state.LoadedAt) > 3*time.Second {
		state.loading = true
		app.memoryCall("memoryGet", manager.Request{Project: n.Project, Scope: n.Scope, ID: n.ID, Version: n.Version}, func(raw json.RawMessage, err error) {
			state.loading = false
			state.LoadedAt = time.Now()
			state.Entry = memory.Entry{}
			if err != nil {
				state.err = err.Error()
				return
			}
			state.err = ""
			_ = json.Unmarshal(raw, &state.Entry)
		})
	}
	c.After(3 * time.Second)
	ui.Column(c).FillWidth().Padding(12).Gap(6).Background(widgets.Bubble).Radius(10).Children(func() {
		scope := tr("Current project")
		if n.Scope == "user" {
			scope = tr("User preferences (across projects)")
		}
		ui.Text(c, tr("Miya memory · ")+scope).FontSize(12).FontWeight(600)
		if state.err != "" {
			ui.Text(c, state.err).FontSize(12).TextColor(widgets.Muted)
			return
		}
		e := state.Entry
		if e.ID == "" {
			ui.Text(c, tr("Loading memory…")).FontSize(12).TextColor(widgets.Muted)
			return
		}
		if e.Content != "" {
			ui.Text(c, e.Content).FontSize(12).LineHeight(1.4).Selectable()
		}
		if e.State != "active" && e.State != "expired" && e.State != "conflicted" {
			ui.Text(c, widgets.MemoryStateLabel(e.State)).FontSize(12).TextColor(widgets.Muted)
			return
		}
		if e.State == "conflicted" {
			ui.Text(c, widgets.MemoryStateLabel(e.State)).FontSize(12).TextColor(widgets.Muted)
		}
		if ui.Button(c.Key("undo"), tr("Undo memory change")).Disabled(app.Memories.Busy).Clicked() {
			app.memoryAction("memoryForget", manager.Request{Project: n.Project, Scope: n.Scope, ID: n.ID, Version: n.Version}, nil)
		}
	})
}
