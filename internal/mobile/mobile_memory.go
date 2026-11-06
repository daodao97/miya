package mobile

import (
	"encoding/json"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	"miya/internal/memory/manager"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/widgets"
)

type MobileMemory struct {
	Project, Scope string
	List           manager.List
	loading        bool
	RequestedAt    time.Time
	err            string
}

func (m *App) loadPhoneMemory() {
	state := &m.Memories
	if state.loading || !m.Connected || m.Conn == nil || !m.State.Memory {
		return
	}
	state.loading = true
	state.RequestedAt = time.Now()
	conn, gen := m.Conn, m.Generation
	project, scope := state.Project, state.Scope
	go func() {
		if err := conn.Send(remote.Request{Op: "memory", Project: project, MemoryScope: scope}); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn && state.Project == project && state.Scope == scope {
					state.loading = false
					state.err = err.Error()
				}
			})
		}
	}()
}

func (m *App) ReceiveMemory(raw json.RawMessage, message string) {
	var result phoneproto.MemoryResult
	if err := json.Unmarshal(raw, &result); err != nil {
		m.Memories.loading = false
		m.Memories.err = tr("Failed to load memory")
		return
	}
	state := &m.Memories
	if result.Project != state.Project || result.Scope != state.Scope {
		return
	}
	state.loading = false
	if result.Revision < m.State.MemoryRevision {
		state.RequestedAt = time.Time{}
		return
	}
	if message != "" {
		state.err = message
		return
	}
	state.List = result.List
	state.err = ""
}

func (m *App) memoryPage(c *ui.Context) {
	state := &m.Memories
	project := m.selectedProject().Path
	if state.Project != project {
		*state = MobileMemory{Project: project, Scope: "project"}
	}
	if m.Open != 0 && !m.Router().Transitioning() {
		m.leave()
	}
	if state.loading && time.Since(state.RequestedAt) > 10*time.Second {
		state.loading = false
		state.err = tr("Read timed out. Try again.")
		state.RequestedAt = time.Now()
	}
	if !state.loading && time.Since(state.RequestedAt) > 5*time.Second {
		m.loadPhoneMemory()
	}
	c.After(time.Second)
	p := m.Pal
	ui.Row(c).FillWidth().MinHeight(52).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/memory")
		ui.Text(c, tr("Shared memory")).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		if m.homeAction(c, "refresh", tr("Refresh memory"), "refresh").Disabled(!m.Connected || !m.State.Memory || state.loading).Clicked() {
			m.loadPhoneMemory()
		}
	})
	ui.Scroll(c.Key("memories-content")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16, 20, 24, 20).Gap(14).Children(func() {
			ui.Text(c, tr("Read-only on mobile. Edit or forget memories on your desktop. Forgetting does not delete agent history or backups.")).FontSize(m.Font(13)).TextColor(p.muted).LineHeight(1.5)
			ui.Row(c).FillWidth().Wrap().Gap(8).Role(ui.RoleRadioGroup).Label(tr("Shared memory")).Children(func() {
				for _, scope := range []string{"project", "user"} {
					label := tr("Current project")
					if scope == "user" {
						label = tr("User preferences")
					}
					if m.pill(c, scope, label, state.Scope == scope).Role(ui.RoleRadio).Checked(state.Scope == scope).Clicked() {
						state.Scope = scope
						state.List = manager.List{}
						state.loading = false
						state.RequestedAt = time.Time{}
					}
				}
			})
			if !m.Connected {
				ui.Text(c, tr("Connect to your desktop to view memory.")).TextColor(p.muted)
				return
			}
			if !m.State.Memory {
				ui.Text(c, tr("Update the desktop app to view memory.")).TextColor(p.muted)
				return
			}
			if state.err != "" {
				ui.Text(c, state.err).TextColor(widgets.Danger)
				return
			}
			if len(state.List.Entries) == 0 {
				ui.Text(c, tr("No memories yet.")).TextColor(p.muted)
			}
			for _, e := range state.List.Entries {
				ui.Column(c.Key(e.ID)).FillWidth().Padding(16).Gap(8).Radius(14).Background(p.surface).Children(func() {
					ui.Text(c, widgets.MemoryKindLabel(e.Kind)+" · "+widgets.MemoryStateLabel(e.State)).FontSize(m.Font(13)).TextColor(p.muted)
					if e.Content != "" {
						ui.Text(c, e.Content).FontSize(m.Font(15)).LineHeight(1.5).Selectable()
					}
					ui.Text(c, tr("As of ")+widgets.MemoryTime(e.ObservedAt)+tr("\nExpires ")+widgets.MemoryTime(e.ValidUntil)).FontSize(m.Font(12)).TextColor(p.muted)
					for i, src := range e.Sources {
						ui.Column(c.Key(i)).Gap(6).Children(func() {
							if src.Quote != "" {
								ui.Text(c, src.Quote).FontSize(m.Font(13)).TextColor(p.muted).LineHeight(1.5).Selectable()
							}
							ref, ok := model.ParseThreadURL(src.Thread)
							if !ok {
								return
							}
							for _, project := range m.State.Projects {
								for _, thread := range project.Threads {
									if (model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(thread), SessionID: thread.ServerID}) != ref {
										continue
									}
									if m.phoneButton(c.Key("source")).MinHeight(44).Label(tr("Source conversation")).Children(func() { ui.Text(c, tr("Source conversation")).FontSize(m.Font(14)) }).Clicked() {
										m.OpenThread(thread)
									}
								}
							}
						})
					}
				})
			}
		})
	})
}
