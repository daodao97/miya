package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/memory"
)

type memoryUI struct {
	scope, project             string
	list                       memoryList
	loadedAt                   time.Time
	loading, busy              bool
	err                        string
	editing                    bool
	id                         string
	version                    uint64
	kind, content, task, until string
	file                       string
	promotion                  *memoryPromotion
	filter                     string
}
type memoryNoticeUI struct {
	entry    memory.Entry
	loadedAt time.Time
	loading  bool
	err      string
}

func (app *chatApp) memoryCall(op string, r memoryRequest, done func(json.RawMessage, error)) {
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
			err := cc.remote.call(op, r, &raw)
			cc.dispatch(func() { done(raw, err) })
		}()
		return
	}
	v, err := cc.memoryOp(op, r)
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
	app.memoryCall("memoryList", memoryRequest{Project: project, Scope: scope}, func(raw json.RawMessage, err error) {
		// Don't display a previous project's results after navigation.
		if app.c.cwd != project || m.scope != scope || revision != app.c.memoryRevision {
			m.loading = false
			return
		}
		m.loading = false
		m.loadedAt = time.Now()
		if err != nil {
			m.list = memoryList{}
			m.err = err.Error()
			return
		}
		var next memoryList
		if err = json.Unmarshal(raw, &next); err != nil {
			m.err = err.Error()
			return
		}
		m.list = next
		m.err = ""
	})
}
func (app *chatApp) memoryAction(op string, r memoryRequest, done func(json.RawMessage)) {
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
	if !m.loading && time.Since(m.loadedAt) > 5*time.Second {
		app.loadMemory()
	}
	c.After(time.Second)
	ui.Column(c).FillWidth().MaxWidth(880).AlignSelf(ui.Center).Gap(14).Children(func() {
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "共享记忆").FontSize(18).FontWeight(600).Grow(1)
			for _, scope := range []string{"project", "user"} {
				label := "当前项目"
				if scope == "user" {
					label = "用户偏好"
				}
				button := ui.Button(c.Key(scope), label).Disabled(m.busy || m.editing)
				if m.scope == scope {
					button.Background(chosen)
				}
				if button.Clicked() {
					m.scope = scope
					m.list = memoryList{}
					m.loadedAt = time.Time{}
				}
			}
			if ui.Button(c.Key("refresh"), "刷新").Disabled(m.loading || m.busy).Clicked() {
				app.loadMemory()
			}
		})
		ui.Text(c, "本机保存，Codex 与 Claude 共享。当前要求和项目文件优先。遗忘会清除 Miya 正文，不会删除 Agent 历史或备份。").FontSize(12).TextColor(muted).LineHeight(1.5)
		if m.scope != "user" {
			card(c.Key("memory-settings")).Gap(16).Children(func() {
				enabled := !m.list.Settings.Disabled
				ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(10).Children(func() {
					if ui.Switch(c.Key("enabled"), &enabled).Label("在当前项目使用记忆工具").Disabled(m.busy || m.loading).Changed() {
						settings := m.list.Settings
						settings.Disabled = !enabled
						app.memoryAction("memoryConfigure", memoryRequest{Project: app.c.cwd, Settings: &settings}, nil)
					}
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, "在当前项目使用记忆工具").FontSize(13)
						ui.Text(c, "项目总开关：允许 Agent 查询、保存、纠正和忘记记忆，并使用下方自动功能。关闭后保留已有记忆，仍可在此管理。").FontSize(12).TextColor(muted).LineHeight(1.5).FillWidth().Wrap()
					})
				})
				useUser := m.list.Settings.UseUserPreferences
				ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(10).Children(func() {
					if ui.Switch(c.Key("user-preferences"), &useUser).Label("查询时包含用户偏好").Disabled(m.busy || m.loading).Changed() {
						settings := m.list.Settings
						settings.UseUserPreferences = useUser
						app.memoryAction("memoryConfigure", memoryRequest{Project: app.c.cwd, Settings: &settings}, nil)
					}
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, "查询时包含用户偏好").FontSize(13)
						ui.Text(c, "检索当前项目记忆时，一并查找「用户偏好」中保存的跨项目偏好，例如解释语言和个人习惯。").FontSize(12).TextColor(muted).LineHeight(1.5).FillWidth().Wrap()
					})
				})
				inject := m.list.Settings.AutoInject
				ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(10).Children(func() {
					if ui.Switch(c.Key("auto-inject"), &inject).Label("发送时自动召回相关记忆").Disabled(m.busy || m.loading || !enabled).Changed() {
						settings := m.list.Settings
						settings.AutoInject = inject
						app.memoryAction("memoryConfigure", memoryRequest{Project: app.c.cwd, Settings: &settings}, nil)
					}
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, "发送时自动召回相关记忆").FontSize(13)
						ui.Text(c, "每次发送前，在本地查找相关记忆并附给当前 Agent。查找不额外调用模型；附加内容会占用对话上下文。").FontSize(12).TextColor(muted).LineHeight(1.5).FillWidth().Wrap()
					})
				})
				extract := m.list.Settings.AutoExtract
				ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(10).Children(func() {
					if ui.Switch(c.Key("auto-extract"), &extract).Label("回合完成后自动提炼记忆").Disabled(m.busy || m.loading || !enabled || !m.list.ExtractorAvailable && !m.list.Settings.AutoExtract).Changed() {
						settings := m.list.Settings
						settings.AutoExtract = extract
						app.memoryAction("memoryConfigure", memoryRequest{Project: app.c.cwd, Settings: &settings}, nil)
					}
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, "回合完成后自动提炼记忆").FontSize(13)
						ui.Text(c, "成功回合结束后，使用选定的 Agent 提炼并保存值得保留的信息，每轮最多三项，可撤销。会使用所选 Agent 的额度。").FontSize(12).TextColor(muted).LineHeight(1.5).FillWidth().Wrap()
						if len(m.list.Extractors) > 0 {
							ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(6).Margin(4, 0, 0, 0).Children(func() {
								ui.Text(c, "提炼 Agent").FontSize(12).TextColor(muted)
								for _, option := range m.list.Extractors {
									agent := option.Agent
									button := quiet(c.Key("extractor:" + agent)).Height(28).PaddingX(9).Gap(6).Label("记忆提炼 Agent：" + backendFor(agent).Name()).Disabled(m.busy || m.loading)
									if agent == m.list.Settings.ExtractionAgent() {
										button.Background(chosen)
									}
									button.Children(func() {
										agentIcon(c, agent, 16)
										ui.Text(c, backendFor(agent).Name()).FontSize(12)
										if !option.Available {
											ui.Text(c, "未配置").FontSize(11).TextColor(muted)
										}
									})
									if button.Clicked() && agent != m.list.Settings.ExtractionAgent() {
										settings := m.list.Settings
										settings.ExtractorAgent = agent
										app.memoryAction("memoryConfigure", memoryRequest{Project: app.c.cwd, Settings: &settings}, nil)
									}
								}
							})
							ui.Text(c, "独立于对话 Agent，按项目保存，使用所选 Agent 的默认模型。").FontSize(12).TextColor(muted).LineHeight(1.5).FillWidth().Wrap()
						} else {
							ui.Text(c, "当前后台尚不支持选择提炼 Agent，升级后可配置。").FontSize(12).TextColor(muted).FillWidth().Wrap()
						}
						if !m.list.ExtractorAvailable {
							ui.Text(c, "当前没有可用的 "+backendFor(m.list.Settings.ExtractionAgent()).Name()+"，自动提炼暂不可用。").FontSize(12).TextColor(muted).FillWidth().Wrap()
						}
					})
				})
				if cur := app.c.current(); cur.ID != 0 {
					sessionEnabled := !cur.MemoryDisabled
					ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(10).Children(func() {
						if ui.Switch(c.Key("session-memory"), &sessionEnabled).Label("在当前会话使用自动记忆").Disabled(m.busy || m.loading).Changed() {
							disabled := !sessionEnabled
							app.memoryAction("memorySessionConfigure", memoryRequest{Project: app.c.cwd, Thread: cur.ID, SessionDisabled: &disabled}, nil)
						}
						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							ui.Text(c, "在当前会话使用自动记忆").FontSize(13)
							ui.Text(c, "跟随项目的自动召回、自动提炼开关。关闭后只停用当前会话的自动功能，手动记忆工具仍可使用。").FontSize(12).TextColor(muted).LineHeight(1.5).FillWidth().Wrap()
						})
					})
				}
				status := m.list.Extraction
				if status.Pending+status.Completed+status.Failed > 0 {
					ui.Text(c, fmt.Sprintf("提炼：等待 %d · 完成 %d · 跳过或失败 %d · 已保存 %d · 校验拒绝 %d", status.Pending, status.Completed, status.Failed, status.Accepted, status.Rejected)).FontSize(12).TextColor(muted).Wrap()
				}
			})
		}
		if m.err != "" {
			ui.Text(c, m.err).TextColor(danger).Selectable()
		}
		if m.editing {
			app.memoryForm(c)
			return
		}
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.TextInput(c.Key("filter"), &m.filter).Placeholder("筛选正文、类型或状态").Label("筛选记忆").Grow(1)
			if ui.PrimaryButton(c.Key("new"), "新增记忆").Disabled(m.busy).Clicked() {
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
		if len(m.list.Entries) == 0 {
			ui.Text(c, "还没有记忆。可以在这里新增，或在对话中明确要求 Agent 记住。").TextColor(muted)
		}
		for _, e := range m.list.Entries {
			if m.filter != "" && !strings.Contains(strings.ToLower(e.Content+" "+e.Kind+" "+e.State), strings.ToLower(m.filter)) {
				continue
			}
			app.memoryRow(c.Key(e.ID), e)
		}
	})
}
func memoryKindLabel(kind string) string {
	switch kind {
	case "preference":
		return "偏好"
	case "fact":
		return "事实"
	case "progress":
		return "进展"
	}
	return kind
}
func memoryStateLabel(state string) string {
	switch state {
	case "active":
		return "有效"
	case "expired":
		return "已过期"
	case "forgotten":
		return "已忘记"
	case "promoted":
		return "已提升"
	case "superseded":
		return "已更新"
	case "conflicted":
		return "有冲突，待核实"
	}
	return state
}
func memoryTime(t time.Time) string {
	if t.IsZero() {
		return "未设置"
	}
	return t.Local().Format("2006-01-02 15:04")
}
func (app *chatApp) memoryRow(c *ui.Context, e memory.Entry) {
	m := &app.memories
	card(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).Wrap().Gap(8).Children(func() {
			if e.Kind != "" {
				badge(c, memoryKindLabel(e.Kind))
			}
			badge(c, memoryStateLabel(e.State))
			ui.Text(c, fmt.Sprintf("版本 %d", e.Version)).FontSize(12).TextColor(muted)
		})
		if e.Content != "" {
			ui.Text(c, e.Content).LineHeight(1.5).Selectable()
		}
		ui.Text(c, "信息时间 "+memoryTime(e.ObservedAt)+" · 有效期 "+memoryTime(e.ValidUntil)).FontSize(12).TextColor(muted)
		if e.TaskKey != "" {
			ui.Text(c, "任务："+e.TaskKey).FontSize(12).TextColor(muted)
		}
		for i, src := range e.Sources {
			app.memorySourceView(c.Key(i), src)
		}
		if e.State == "forgotten" {
			return
		}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			if ui.Button(c.Key("edit"), "编辑").Disabled(m.busy || e.State == "promoted").Clicked() {
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
			req := memoryRequest{Project: app.c.cwd, Scope: m.scope, ID: e.ID, Version: e.Version, Thread: app.c.active()}
			if ui.Button(c.Key("forget"), "忘记").Disabled(m.busy).Clicked() {
				app.memoryAction("memoryForget", req, nil)
			}
			if e.Scope.Kind == "project" && e.Kind == "preference" && e.State == "active" {
				if ui.Button(c.Key("user"), "提升到用户级").Disabled(m.busy).Clicked() {
					app.memoryAction("memoryPromoteUser", req, nil)
				}
			}
			if e.Scope.Kind == "project" && e.Kind == "fact" && e.State == "active" {
				if ui.Button(c.Key("promote"), "提升到项目文件…").Disabled(m.busy).Clicked() {
					m.id = e.ID
					m.version = e.Version
					m.content = e.Content
					m.kind = e.Kind
					m.file = "AGENTS.md"
					m.promotion = &memoryPromotion{}
				}
			}
		})
		if m.promotion != nil && m.id == e.ID {
			app.memoryPromotionView(c, e)
		}
	})
}
func (app *chatApp) memorySourceView(c *ui.Context, src memory.Source) {
	label := map[string]string{"manual": "管理界面编辑", "user_statement": "用户声明", "assistant_report": "助手报告", "tool_result": "工具结果"}[src.EvidenceKind]
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label+" · "+memoryTime(src.ObservedAt)).FontSize(12).TextColor(muted)
		if src.Thread != "" {
			if ui.Button(c.Key("source"), "来源会话").Clicked() {
				app.openMemorySource(src.Thread)
			}
			if src.Limited {
				ui.Text(c, "后端未提供稳定消息 ID，定位能力有限").FontSize(11).TextColor(muted)
			}
		}
		if src.Quote != "" {
			ui.Text(c, src.Quote).FontSize(12).TextColor(muted).LineHeight(1.4).Selectable()
		}
	})
}
func (app *chatApp) openMemorySource(raw string) {
	ref, ok := parseThreadURL(raw)
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
	app.notice = "来源会话当前不可用；记忆保留了原始来源标识。"
}
func (app *chatApp) memoryForm(c *ui.Context) {
	m := &app.memories
	card(c).Gap(12).Children(func() {
		title := "新增记忆"
		if m.id != "" {
			title = "编辑记忆"
		}
		ui.Text(c, title).FontWeight(600)
		ui.Row(c).Gap(8).Children(func() {
			for _, kind := range []string{"preference", "fact", "progress"} {
				if m.scope == "user" && kind != "preference" {
					continue
				}
				if ui.Button(c.Key(kind), memoryKindLabel(kind)).Disabled(m.busy).Clicked() {
					m.kind = kind
				}
			}
			badge(c, memoryKindLabel(m.kind))
		})
		ui.TextArea(c.Key("content"), &m.content).Lines(3, 10).FillWidth().Label("记忆正文").Placeholder("需要保存的信息")
		if m.kind == "progress" {
			ui.TextInput(c.Key("task"), &m.task).FillWidth().Label("任务标识").Placeholder("任务标识，例如 issue-123")
		}
		ui.TextInput(c.Key("until"), &m.until).FillWidth().Label("有效期").Placeholder("可选 RFC3339 时间；留空长期有效，进展默认 7 天")
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c.Key("save"), "保存记忆").Disabled(m.busy || strings.TrimSpace(m.content) == "").Clicked() {
				app.memoryAction("memorySave", memoryRequest{Project: app.c.cwd, Scope: m.scope, ID: m.id, Version: m.version, Kind: m.kind, Content: m.content, TaskKey: m.task, ValidUntil: m.until, Thread: app.c.active()}, func(json.RawMessage) { m.editing = false })
			}
			if ui.Button(c.Key("cancel"), "取消").Disabled(m.busy).Clicked() {
				m.editing = false
			}
		})
	})
}
func (app *chatApp) memoryPromotionView(c *ui.Context, e memory.Entry) {
	m := &app.memories
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "确认目标和措辞后写入项目文件，原记忆将停止召回。").FontSize(12).TextColor(muted)
		ui.Row(c).Gap(8).Children(func() {
			for _, file := range []string{"AGENTS.md", "CLAUDE.md"} {
				if ui.Button(c.Key(file), file).Disabled(m.busy).Clicked() {
					m.file = file
					m.promotion = &memoryPromotion{}
				}
			}
			badge(c, m.file)
		})
		if ui.TextArea(c.Key("wording"), &m.content).Lines(2, 6).FillWidth().Label("建议措辞").Changed() {
			m.promotion = &memoryPromotion{}
		}
		req := memoryRequest{Project: app.c.cwd, ID: e.ID, Version: e.Version, File: m.file, Content: m.content, Thread: app.c.active()}
		if ui.Button(c.Key("preview"), "预览文件修改").Disabled(m.busy || strings.TrimSpace(m.content) == "").Clicked() {
			app.memoryAction("memoryPromotionPreview", req, func(raw json.RawMessage) {
				var p memoryPromotion
				if json.Unmarshal(raw, &p) == nil {
					m.promotion = &p
				}
			})
		}
		if m.promotion.After != "" {
			ui.Text(c, "写入后的 "+m.promotion.File).FontSize(12).FontWeight(600)
			ui.Scroll(c.Key("preview-content")).MaxHeight(240).FillWidth().Children(func() { ui.Text(c, m.promotion.After).FontSize(12).LineHeight(1.5).Selectable() })
			if ui.PrimaryButton(c.Key("confirm"), "确认写入项目文件").Disabled(m.busy).Clicked() {
				b, _ := json.Marshal(m.promotion)
				req.Preview = string(b)
				app.memoryAction("memoryPromoteFile", req, func(json.RawMessage) { m.promotion = nil })
			}
		}
		if ui.Button(c.Key("cancel"), "取消提升").Disabled(m.busy).Clicked() {
			m.promotion = nil
		}
	})
}
func (app *chatApp) memoryNoticeView(c *ui.Context, n *memoryNotice) {
	if n == nil {
		return
	}
	if app.mobile {
		ui.Text(c, "Miya 记忆已变更，可在记忆页查看；编辑请使用桌面端。").FontSize(12).TextColor(muted)
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
		app.memoryCall("memoryGet", memoryRequest{Project: n.Project, Scope: n.Scope, ID: n.ID, Version: n.Version}, func(raw json.RawMessage, err error) {
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
		scope := "当前项目"
		if n.Scope == "user" {
			scope = "用户级偏好（跨项目）"
		}
		ui.Text(c, "Miya 记忆 · "+scope).FontSize(12).FontWeight(600)
		if state.err != "" {
			ui.Text(c, state.err).FontSize(12).TextColor(muted)
			return
		}
		e := state.entry
		if e.ID == "" {
			ui.Text(c, "正在读取记忆…").FontSize(12).TextColor(muted)
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
		if ui.Button(c.Key("undo"), "撤销记忆").Disabled(app.memories.busy).Clicked() {
			app.memoryAction("memoryForget", memoryRequest{Project: n.Project, Scope: n.Scope, ID: n.ID, Version: n.Version}, nil)
		}
	})
}
