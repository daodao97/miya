package nsmobile

import (
	"fmt"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/chat/model"
	memorypresentation "miya/internal/memory/presentation"
	phoneclient "miya/internal/phone/client"
)

func (m *App) memorySheet(cx *core.Scope) core.Node {
	project := m.SelectedProject().Path
	if m.Memories.Project != project || m.Memories.Scope == "" {
		m.Memories = phoneclient.Memory{Project: project, Scope: "project"}
	}
	m.pollSheet(cx, func() {
		if m.Memories.Revision < m.State.MemoryRevision || time.Since(m.Memories.RequestedAt) > 5*time.Second {
			m.LoadMemory()
		}
	})
	return scroll(text(tr("Shared memory")), m.sheetBack("memory-close", tr("Done"), ""),
		text(tr("Read-only on mobile. Edit or forget memories on your desktop. Forgetting does not delete agent history or backups.")),
		row(m.button("memory-project", tr("Current project"), nil, func() { m.Memories = phoneclient.Memory{Project: project, Scope: "project"}; m.LoadMemory() }), m.button("memory-user", tr("User preferences"), nil, func() { m.Memories = phoneclient.Memory{Project: project, Scope: "user"}; m.LoadMemory() })),
		m.button("memory-refresh", tr("Refresh memory"), live(m, func() bool { return !m.Connected || !m.State.Memory || m.Memories.Loading }), m.LoadMemory),
		core.Text{ContentFunc: live(m, func() string {
			if !m.Connected {
				return tr("Connect to your desktop to view memory.")
			}
			if !m.State.Memory {
				return tr("Update the desktop app to view memory.")
			}
			if m.Memories.Error != "" {
				return m.Memories.Error
			}
			if m.Memories.Loading {
				return tr("Loading…")
			}
			if len(m.Memories.List.Entries) == 0 {
				return tr("No memories yet.")
			}
			return ""
		})},
		core.Dynamic(func(cx *core.Scope) core.Node {
			m.revision.Get()
			var nodes []core.Node
			if !m.State.Memory {
				return core.View{}
			}
			for _, e := range m.Memories.List.Entries {
				children := []core.Node{text(memorypresentation.MemoryKindLabel(e.Kind) + " · " + memorypresentation.MemoryStateLabel(e.State)), text(e.Content), text(tr("As of ") + memorypresentation.MemoryTime(e.ObservedAt) + tr("\nExpires ") + memorypresentation.MemoryTime(e.ValidUntil))}
				for i, src := range e.Sources {
					children = append(children, text(src.Quote))
					if ref, ok := model.ParseThreadURL(src.Thread); ok {
						if t, _, found := m.ReferenceThread(ref); found {
							children = append(children, m.button(fmt.Sprintf("memory-source-%s-%d", e.ID, i), tr("Source conversation"), nil, func() { m.OpenThread(t); m.pages.Open("") }))
						}
					}
				}
				nodes = append(nodes, core.View{TestID: "memory-entry-" + e.ID, Style: core.Style{Padding: core.All(12), Gap: 8, BorderWidth: 1, CornerRadius: 12}, Children: children})
			}
			return column(nodes...)
		}))
}
