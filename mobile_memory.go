package main

import (
	"encoding/json"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/memory/manager"
	"miya/internal/model"
	"miya/internal/remote"
)

type mobileMemory struct {
	project, scope string
	list           manager.List
	loading        bool
	requestedAt    time.Time
	err            string
}

func (m *mobileApp) loadPhoneMemory() {
	state := &m.memories
	if state.loading || !m.connected || m.conn == nil || !m.state.Memory {
		return
	}
	state.loading = true
	state.requestedAt = time.Now()
	conn, gen := m.conn, m.generation
	project, scope := state.project, state.scope
	go func() {
		if err := conn.Send(remote.Request{Op: "memory", Project: project, MemoryScope: scope}); err != nil {
			m.onMain(gen, func() {
				if m.conn == conn && state.project == project && state.scope == scope {
					state.loading = false
					state.err = err.Error()
				}
			})
		}
	}()
}

func (m *mobileApp) receiveMemory(raw json.RawMessage, message string) {
	var result phoneMemoryResult
	if err := json.Unmarshal(raw, &result); err != nil {
		m.memories.loading = false
		m.memories.err = tr("Failed to load memory")
		return
	}
	state := &m.memories
	if result.Project != state.project || result.Scope != state.scope {
		return
	}
	state.loading = false
	if result.Revision < m.state.MemoryRevision {
		state.requestedAt = time.Time{}
		return
	}
	if message != "" {
		state.err = message
		return
	}
	state.list = result.List
	state.err = ""
}

func (m *mobileApp) memoryPage(c *ui.Context) {
	state := &m.memories
	project := m.selectedProject().Path
	if state.project != project {
		*state = mobileMemory{project: project, scope: "project"}
	}
	if m.open != 0 && !m.router().Transitioning() {
		m.leave()
	}
	if state.loading && time.Since(state.requestedAt) > 10*time.Second {
		state.loading = false
		state.err = tr("Read timed out. Try again.")
		state.requestedAt = time.Now()
	}
	if !state.loading && time.Since(state.requestedAt) > 5*time.Second {
		m.loadPhoneMemory()
	}
	c.After(time.Second)
	p := m.pal
	ui.Row(c).FillWidth().MinHeight(52).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/memory")
		ui.Text(c, tr("Shared memory")).Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		if m.homeAction(c, "refresh", tr("Refresh memory"), "refresh").Disabled(!m.connected || !m.state.Memory || state.loading).Clicked() {
			m.loadPhoneMemory()
		}
	})
	ui.Scroll(c.Key("memories-content")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16, 20, 24, 20).Gap(14).Children(func() {
			ui.Text(c, tr("Read-only on mobile. Edit or forget memories on your desktop. Forgetting does not delete agent history or backups.")).FontSize(m.font(13)).TextColor(p.muted).LineHeight(1.5)
			ui.Row(c).FillWidth().Wrap().Gap(8).Role(ui.RoleRadioGroup).Label(tr("Shared memory")).Children(func() {
				for _, scope := range []string{"project", "user"} {
					label := tr("Current project")
					if scope == "user" {
						label = tr("User preferences")
					}
					if m.pill(c, scope, label, state.scope == scope).Role(ui.RoleRadio).Checked(state.scope == scope).Clicked() {
						state.scope = scope
						state.list = manager.List{}
						state.loading = false
						state.requestedAt = time.Time{}
					}
				}
			})
			if !m.connected {
				ui.Text(c, tr("Connect to your desktop to view memory.")).TextColor(p.muted)
				return
			}
			if !m.state.Memory {
				ui.Text(c, tr("Update the desktop app to view memory.")).TextColor(p.muted)
				return
			}
			if state.err != "" {
				ui.Text(c, state.err).TextColor(danger)
				return
			}
			if len(state.list.Entries) == 0 {
				ui.Text(c, tr("No memories yet.")).TextColor(p.muted)
			}
			for _, e := range state.list.Entries {
				ui.Column(c.Key(e.ID)).FillWidth().Padding(16).Gap(8).Radius(14).Background(p.surface).Children(func() {
					ui.Text(c, memoryKindLabel(e.Kind)+" · "+memoryStateLabel(e.State)).FontSize(m.font(13)).TextColor(p.muted)
					if e.Content != "" {
						ui.Text(c, e.Content).FontSize(m.font(15)).LineHeight(1.5).Selectable()
					}
					ui.Text(c, tr("As of ")+memoryTime(e.ObservedAt)+tr("\nExpires ")+memoryTime(e.ValidUntil)).FontSize(m.font(12)).TextColor(p.muted)
					for i, src := range e.Sources {
						ui.Column(c.Key(i)).Gap(6).Children(func() {
							if src.Quote != "" {
								ui.Text(c, src.Quote).FontSize(m.font(13)).TextColor(p.muted).LineHeight(1.5).Selectable()
							}
							ref, ok := model.ParseThreadURL(src.Thread)
							if !ok {
								return
							}
							for _, project := range m.state.Projects {
								for _, thread := range project.Threads {
									if (ThreadRef{Source: m.state.ReferenceSource, Backend: model.ThreadAgent(thread), SessionID: thread.ServerID}) != ref {
										continue
									}
									if m.phoneButton(c.Key("source")).MinHeight(44).Label(tr("Source conversation")).Children(func() { ui.Text(c, tr("Source conversation")).FontSize(m.font(14)) }).Clicked() {
										m.openThread(thread)
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
