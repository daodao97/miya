package nsui

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/agent"
	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	projectfiles "miya/internal/project/files"
	svcproto "miya/internal/service/protocol"
)

type composerCompletion struct {
	d           *desktopComposer
	token       composer.ComposerToken
	source      string
	generation  int
	cancel      context.CancelFunc
	pendingMove int
	open        *core.Signal[bool]
	choices     *core.Signal[[]model.ComposerChoice]
	selected    *core.Signal[int]
	status      *core.Signal[string]
	files       *core.Signal[[]string]
}

func newComposerCompletion(cx *core.Scope, d *desktopComposer) *composerCompletion {
	m := &composerCompletion{d: d, files: core.NewSignalFunc(cx, []string(nil), slices.Equal), open: core.NewSignal(cx, false), choices: core.NewSignalFunc(cx, []model.ComposerChoice(nil), func(a, b []model.ComposerChoice) bool { return false }), selected: core.NewSignal(cx, 0), status: core.NewSignal(cx, "")}
	cx.Cleanup(func() { m.close() })
	return m
}
func (m *composerCompletion) close() {
	m.generation++
	m.pendingMove = 0
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.open.Set(false)
}
func (m *composerCompletion) visible() bool {
	t := m.d.host.Current()
	return m.open.Get() && m.source == m.d.draft() && !t.ReadOnly && !t.Loading
}
func (m *composerCompletion) edit(value string) {
	m.editAt(value, composer.ComposerEditEnd(m.d.draft(), value))
}
func (m *composerCompletion) editAt(value string, end int) {
	c := m.d.host
	c.RememberDraft(value)
	m.d.notice.Set("")
	m.close()
	m.d.menu.Set("")
	m.token = composer.ComposerTokenAt(value, end)
	m.source = value
	m.selected.Set(0)
	m.choices.Set(nil)
	if m.token.Kind == 0 {
		return
	}
	cap := c.CollaborationCapabilities()
	if m.token.Kind == '#' && (cap == nil || cap.Version < 1) {
		return
	}
	m.open.Set(true)
	switch m.token.Kind {
	case '/':
		m.status.Set(tr("No matching commands"))
		return // command metadata remains live in items
	case '#':
		m.choices.Set(composer.AgentChoices(m.token.Query, c.EnabledAgents()))
		return
	}
	var refs []model.ComposerChoice
	if cap != nil && cap.Version >= 1 {
		refs = composer.AgentChoices(m.token.Query, c.EnabledAgents())
	}
	active := c.Current().ID
	for _, match := range appmodel.MatchingThreadRefs(c.ThreadRefProjects(), active, m.token.Query) {
		ref := c.ThreadRef(match.Thread)
		if ref.Valid() {
			refs = append(refs, model.ComposerChoice{Name: appmodel.ThreadRefTitle(match.Thread), Detail: tr("Conversation · ") + model.AgentName(model.ThreadAgent(match.Thread)) + " · " + match.ProjectName, Ref: &ref})
		}
	}
	m.choices.Set(refs)
	m.status.Set(tr("Searching project files…"))
	parent := c.State().Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	m.cancel = cancel
	generation, project, source, thread, query := m.generation, c.State().Cwd, c.State().ReferenceSource, c.Current().ID, m.token.Query
	remote := c.State().Remote
	go func() {
		defer cancel()
		timer := time.NewTimer(120 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		var files []string
		var err error
		if remote == nil {
			files, err = projectfiles.IndexProjectFiles(ctx, project)
		} else {
			err = remote.Call("host", svcproto.HostRequest{Action: "index", Project: project}, &files)
		}
		matches := composer.FileChoices(files, query)
		m.d.cx.Post(func() {
			if generation != m.generation || c.State().Cwd != project || c.State().ReferenceSource != source || c.Current().ID != thread {
				return
			}
			m.cancel = nil
			move := m.pendingMove
			m.pendingMove = 0
			if err != nil {
				m.status.Set(tr("Unable to search files: ") + err.Error())
				return
			}
			items := append(slices.Clone(refs), matches...)
			m.choices.Set(items)
			if len(items) > 0 && move != 0 {
				m.selected.Set((move%len(items) + len(items)) % len(items))
			}
			m.status.Set(tr("No matching files"))
		})
	}()
}
func (m *composerCompletion) items() []model.ComposerChoice {
	if m.token.Kind == '/' {
		return m.d.host.CurrentAgentControls().Commands(m.d.host.State().Cwd, true).Filter(m.token.Query)
	}
	return m.choices.Get()
}
func (m *composerCompletion) submit() bool {
	if !m.visible() {
		return false
	}
	m.choose(m.selected.Get())
	return true
}
func choiceKey(c model.ComposerChoice) string {
	if c.Ref != nil {
		return c.Ref.URL()
	}
	if c.Agent != "" {
		return c.Agent
	}
	return c.Name
}
func (m *composerCompletion) choose(index int) {
	if !m.visible() {
		return
	}
	items := m.items()
	if index < 0 || index >= len(items) {
		return
	}
	choice, value, token := items[index], m.source, m.token
	c := m.d.host
	m.close()
	switch token.Kind {
	case '#':
		c.RememberDraft(composer.InsertAgentReference(value, token, choice.Agent, choice.Name))
	case '@':
		if choice.Agent != "" {
			c.RememberDraft(composer.InsertAgentReference(value, token, choice.Agent, choice.Name))
		} else if choice.Ref != nil {
			c.RememberDraft(composer.InsertThreadReference(value, token, *choice.Ref, choice.Name))
			c.PrefetchReference(*choice.Ref)
		} else {
			if !slices.Contains(m.files.Get(), choice.Name) {
				m.files.Set(append(append([]string(nil), m.files.Get()...), choice.Name))
			}
			c.RememberDraft(composer.InsertFileReference(value, token, choice.Name))
		}
	case '/':
		if choice.Name == "mention" {
			m.edit("@")
		} else if agent.IsHostCommand(choice.Name) {
			c.RememberDraft("/" + choice.Name)
			m.d.send(false)
		} else {
			c.RememberDraft("/" + choice.Name + " ")
		}
	}
}
func (m *composerCompletion) view() core.Node {
	return core.Show{When: Live(m.visible), Then: func(cx *core.Scope) core.Node {
		colors := nswidgets.ColorsOf(cx)
		return core.View{TestID: "composer-completion", StyleFunc: nswidgets.CardStyle(cx), Children: []core.Node{
			nswidgets.MutedText(cx, func() string {
				switch m.token.Kind {
				case '/':
					return tr("Commands")
				case '#':
					return tr("Agents")
				}
				return tr("Agents, conversations and project files")
			}),
			core.Each(Live(m.items), choiceKey, func(cx *core.Scope, get func() model.ComposerChoice, index func() int) core.Node {
				key := core.Untracked(cx, func() string { return choiceKey(get()) })
				text := func() string {
					item := get()
					prefix := ""
					if m.token.Kind == '/' {
						prefix = "/"
					}
					if m.token.Kind == '#' {
						prefix = "#"
					}
					return prefix + item.Name + " · " + tr(item.Detail)
				}
				selected := func() bool { return index() == m.selected.Get() }
				return core.View{TestID: "completion-" + key, Role: core.RoleButton, LabelFunc: text, Selected: selected,
					OnPress: func(core.PressEvent) { m.choose(index()) },
					Style:   core.Style{Padding: core.All(6), CornerRadius: 6},
					StyleFunc: func() core.Style {
						return core.Style{Display: displayIf(index()/6 == m.selected.Get()/6),
							Selected: &core.Style{Background: ui.Alpha(colors.Link(), 0.18)}}
					},
					Children: []core.Node{core.Text{ContentFunc: text, Style: core.Style{FontSize: 12}}}}
			}),
			nswidgets.MutedText(cx, Live(func() string {
				if len(m.items()) == 0 {
					if m.token.Kind == '/' {
						catalog := m.d.host.CurrentAgentControls().Commands(m.d.host.State().Cwd, true)
						if !catalog.Known {
							return firstSettingsText(catalog.LoadingMessage, tr("Loading commands…"))
						}
					}
					return m.status.Get()
				}
				return fmt.Sprintf(tr("↑ ↓ select · Enter / Tab confirm · Esc close    %d results"), len(m.items()))
			})),
		}}
	}}
}

func (m *composerCompletion) captureKeys() []string {
	if m.visible() {
		return []string{"enter", "tab", "up", "down", "escape"}
	}
	return nil
}
func (m *composerCompletion) onKey(key string) {
	if !m.visible() {
		return
	}
	switch key {
	case "enter", "tab":
		m.submit()
	case "escape":
		m.close()
	case "up", "down":
		step := 1
		if key == "up" {
			step = -1
		}
		if count := len(m.items()); count > 0 {
			m.selected.Set((m.selected.Get() + step + count) % count)
		} else if m.token.Kind == '@' && m.cancel != nil {
			m.pendingMove += step
		}
	}
}
