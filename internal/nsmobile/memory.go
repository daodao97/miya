package nsmobile

import (
	"fmt"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	"miya/internal/memory"
	memorypresentation "miya/internal/memory/presentation"
	"miya/internal/nswidgets"
	phoneclient "miya/internal/phone/client"
)

func (m *App) memorySheet(cx *core.Scope) core.Node {
	project := m.SelectedProject().Path
	if m.Memories.Project != project || m.Memories.Scope == "" {
		m.Memories = phoneclient.Memory{Project: project, Scope: "project"}
	}
	m.pollSheet(cx, "memory", func() {
		if m.Memories.Revision < m.State.MemoryRevision || time.Since(m.Memories.RequestedAt) > 5*time.Second {
			m.LoadMemory()
		}
	})
	status := live(m, func() string {
		switch {
		case !m.Connected:
			return tr("Connect to your desktop to view memory.")
		case !m.State.Memory:
			return tr("Update the desktop app to view memory.")
		case m.Memories.Error != "":
			return m.Memories.Error
		case m.Memories.Loading && len(m.Memories.List.Entries) == 0:
			return tr("Loading…")
		case len(m.Memories.List.Entries) == 0:
			return tr("No memories yet.")
		}
		return ""
	})
	return m.groupedPage(cx,
		ui.Tabs[string]{TestID: "memory-scope", Label: tr("Memory scope"), Equal: true,
			Options: []ui.Option[string]{
				{Value: "project", Label: tr("Current project"), TestID: "memory-project"},
				{Value: "user", Label: tr("User preferences"), TestID: "memory-user"},
			}, Value: live(m, func() string { return m.Memories.Scope }), TabStyle: core.Style{Height: core.Pt(core.Auto), MinHeight: core.Pt(44)},
			OnChange: func(scope string) {
				m.act(func() {
					m.Memories = phoneclient.Memory{Project: project, Scope: scope}
					m.LoadMemory()
				})()
			}},
		groupedState(cx, "bulb", status, live(m, func() bool { return m.Memories.Loading && m.Connected }),
			live(m, func() bool { return m.Memories.Error != "" && m.Connected })),
		core.Show{When: live(m, func() bool { return m.Connected && m.State.Memory && len(m.Memories.List.Entries) > 0 }),
			Then: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{Gap: 12}, Children: []core.Node{
					core.Each(live(m, func() []memory.Entry { return m.Memories.List.Entries }), func(e memory.Entry) string { return e.ID },
						func(cx *core.Scope, e func() memory.Entry, _ func() int) core.Node { return m.memoryEntry(cx, e) }),
				}}
			}},
		groupedFooter(cx, "memory-read-only", func() string { return tr("Edit or forget memories on your desktop.") }),
	)
}

func (m *App) memoryEntry(cx *core.Scope, entry func() memory.Entry) core.Node {
	colors := nswidgets.ColorsOf(cx)
	meta := func(content func() string) core.Node {
		return core.Text{ContentFunc: content, StyleFunc: func() core.Style {
			return core.Style{FontSize: 13, LineHeight: 17, Color: colors.Muted()}
		}}
	}
	id := core.Untracked(cx, entry).ID
	return core.View{TestID: "memory-entry-" + id, Style: core.Style{Padding: core.All(16), Gap: 8, CornerRadius: 14, MinWidth: core.Pt(0)},
		StyleFunc: func() core.Style { return core.Style{Background: groupedSurface(cx)} },
		Children: []core.Node{
			meta(func() string {
				e := entry()
				return memorypresentation.MemoryKindLabel(e.Kind) + " · " + memorypresentation.MemoryStateLabel(e.State)
			}),
			core.Text{Selectable: core.SelectableOn, ContentFunc: func() string { return entry().Content },
				Style: core.Style{FontSize: 16, LineHeight: 23}},
			core.Show{When: func() bool { e := entry(); return !e.ObservedAt.IsZero() || !e.ValidUntil.IsZero() }, Then: func(*core.Scope) core.Node {
				return meta(func() string {
					e := entry()
					asOf, expires := "", ""
					if !e.ObservedAt.IsZero() {
						asOf = tr("As of ") + memorypresentation.MemoryTime(e.ObservedAt)
					}
					if !e.ValidUntil.IsZero() {
						expires = tr("\nExpires ") + memorypresentation.MemoryTime(e.ValidUntil)
					}
					return strings.TrimSpace(asOf + expires)
				})
			}},
			core.Dynamic(func(cx *core.Scope) core.Node {
				var nodes []core.Node
				for i, src := range entry().Sources {
					if src.Quote != "" {
						nodes = append(nodes, core.Text{Content: src.Quote, Selectable: core.SelectableOn, StyleFunc: func() core.Style {
							return core.Style{FontSize: 14, Color: colors.Muted()}
						}})
					}
					if ref, ok := model.ParseThreadURL(src.Thread); ok {
						if t, _, found := m.ReferenceThread(ref); found {
							nodes = append(nodes, m.groupedRow(cx, gRow{ID: fmt.Sprintf("memory-source-%s-%d", id, i), Label: tr("Source conversation"),
								Icon: "message-circle", Chevron: true, Press: func() { m.OpenThread(t); m.pages.Open("") }}))
						}
					}
				}
				return core.Group(nodes)
			}),
		},
	}
}
