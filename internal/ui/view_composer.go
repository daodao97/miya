package desktopui

import (
	"context"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	conversation "miya/internal/chat/view"
	projectfiles "miya/internal/project/files"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

func (app *App) composerArea(c *ui.Context) {
	cc := app.C
	cur := cc.Current()
	ui.Row(c).FillWidth().Justify(ui.Center).Shrink(0).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(800).Padding(0, 28, 10, 28).Gap(10).Children(func() {
			if cur.ID == 0 {
				ui.Text(c, tr("A few ideas to get started")).AlignSelf(ui.End).FontSize(11).TextColor(widgets.Muted)
				ui.Row(c).Gap(10).Children(func() {
					app.suggestion(c.Key("snake"), "device-gamepad-2", tr("Create a classic snake game"), tr("Create a classic snake game in this project."))
					app.suggestion(c.Key("bug"), "bug", tr("Find and fix a bug in my code"), tr("Find and fix a bug in my code. Explain the root cause and verify the fix."))
					app.suggestion(c.Key("pdf"), "file-text", tr("Summarize this app in a PDF"), tr("Summarize this app in a one-page PDF."))
				})
			}
			msg, reconnect := cc.CurrentAgentControls().Problem()
			if msg != "" || len(cc.State().Approvals()) > 0 {
				ui.Column(c).Gap(8).Children(func() {
					if msg != "" {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							widgets.MutedText(c, msg).Grow(1).MinWidth(0)
							if reconnect && widgets.Quiet(c.Key("reconnect")).Children(func() { ui.Text(c, tr("Reconnect")) }).Clicked() {
								cc.CurrentAgentControls().Reconnect()
							}
						})
					}
					for _, a := range cc.State().Approvals() {
						app.approvalView(c.Key("approval:"+string(a.ID)), a)
					}
				})
			}
			app.queueView(c, cur)
			app.taskStatus(c, cur)
			app.collaborationTasks(c, cur)
			if cur.ReadOnly {
				ui.Row(c).AlignItems(ui.Center).Padding(18).Gap(12).Border(1, widgets.Line).Radius(16).Children(func() {
					widgets.Icon(c, "lock", 18, widgets.Muted)
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, tr("Open in another app")).FontWeight(600)
						widgets.MutedText(c, tr("Close the conversation there before continuing here."))
					})
					if widgets.Quiet(c.Key("retry-readonly")).Disabled(cur.Loading).Children(func() { ui.Text(c, tr("Retry")) }).Clicked() {
						cc.Resume(cur, nil)
					}
				})
				return
			}
			app.composerBox(c)
		})
	})
}

func (app *App) suggestion(c *ui.Context, symbol, text, prompt string) {
	b := widgets.Quiet(c).Column().AlignItems(ui.Start).Justify(ui.SpaceBetween).Grow(1).Basis(0).MinWidth(0).Height(82).Padding(14).
		Background(widgets.Subtle).Radius(14)
	if b.Hovered() {
		b.Background(widgets.Hover)
	}
	b.Children(func() {
		widgets.Icon(c, symbol, 20, widgets.Faint)
		ui.Text(c, text).FontSize(11)
	})
	if b.Clicked() {
		app.C.RememberDraft(prompt)
	}
}

func (app *App) composerBox(c *ui.Context) {
	cc := app.C
	cur := cc.Current()
	ui.Column(c).Background(widgets.Panel).Border(1, widgets.Line).Radius(16).Padding(12).Gap(10).Children(func() {
		if paths := cc.State().Attachments(); len(paths) > 0 {
			ui.Row(c).Wrap().Gap(8).FillWidth().MinWidth(0).Children(func() {
				for _, p := range paths {
					app.attachmentPreview(c.Key(p), p)
				}
			})
		}
		app.composerInput(c)
		ui.Row(c).AlignItems(ui.Center).Gap(2).Children(func() {
			if widgets.ToolButton(c.Key("attach"), "plus", tr("Add context")).Clicked() {
				app.attachFiles()
			}
			app.agentPicker(c)
			ui.Spacer(c)
			app.permissionsPicker(c)
			caps := cc.CurrentAgentControls().Caps()
			if caps.Models {
				app.modelPicker(c)
			}
			if caps.Effort {
				app.effortPicker(c)
			}
			hasDraft := strings.TrimSpace(cc.State().Draft()) != "" || len(cc.State().Attachments()) > 0
			stopping := cur.Busy && !hasDraft
			label, symbol := tr("Send message"), "arrow-up"
			switch {
			case stopping:
				label, symbol = tr("Stop response"), "x"
			case cur.Busy:
				label = tr("Add to queue")
			}
			disabled := cur.ReadOnly || cur.TaskPending || !cc.BackendReady() || (!cur.Busy && (cur.Loading || !hasDraft))
			if cur.Busy && hasDraft {
				q := widgets.Quiet(c.Key("steer-draft")).Label(tr("Steer now")).Tooltip(tr("Insert message into the current turn · ⌘Enter"))
				q.Children(func() {
					widgets.Icon(c, "arrow-up", 14, widgets.Muted)
					ui.Text(c, tr("Steer now"))
				})
				if q.Clicked() {
					app.SendDraft(true)
				}
			}
			send := ui.ButtonBase(c.Key("send")).Size(30, 30).Radius(15).Justify(ui.Center).AlignItems(ui.Center).
				Background(widgets.Chosen).Label(label).Tooltip(label).Disabled(disabled)
			if disabled {
				send.Opacity(0.35)
			} else if send.Hovered() {
				send.Background(widgets.SendHover)
			}
			send.Children(func() { widgets.Icon(c, symbol, 18, widgets.Ink) })
			if send.Clicked() {
				if stopping {
					cc.Stop()
				} else {
					app.send()
				}
			}
		})
	})
}

func (app *App) attachmentPreview(c *ui.Context, path string) {
	remove := func() { app.C.RemoveAttachment(path) }
	name := filepath.Base(path)
	if strings.HasPrefix(mime.TypeByExtension(filepath.Ext(path)), "image/") {
		ui.Box(c).Size(160, 120).Shrink(0).Children(func() {
			var img ui.ImageSource
			if strings.HasPrefix(path, "miya-server:") {
				img = app.remoteMessageImage(serverAttachmentSource(path))
			} else {
				img = widgets.CachedImage(path)
			}
			if img != nil {
				ui.Image(c, img).Size(160, 120).Fit(ui.Cover).Radius(8)
			} else {
				ui.Box(c).Size(160, 120).Radius(8).Background(widgets.Bubble)
			}
			x := ui.ButtonBase(c.Key("remove")).Absolute().Top(-3).Right(-3).Size(20, 20).Radius(10).Justify(ui.Center).AlignItems(ui.Center).
				Background(ui.Hex("#303030")).Border(2, widgets.Canvas).Label(tr("Remove attachment ") + name).Tooltip(tr("Remove attachment"))
			if x.Hovered() {
				x.Background(ui.Hex("#505050"))
			}
			x.Children(func() { widgets.Icon(c, "x", 12, widgets.Canvas) })
			x.OnClick(remove)
		})
		return
	}
	ui.Row(c).AlignItems(ui.Center).Gap(4).MinWidth(0).Padding(4).Border(1, widgets.Line).Radius(8).Children(func() {
		ui.Text(c, name).MaxWidth(200).SingleLine().Ellipsis("…").TextColor(widgets.Muted).FontSize(11)
		x := widgets.Quiet(c.Key("remove")).Size(24, 24).PaddingX(3).Justify(ui.Center).Label(tr("Remove attachment ") + name).Tooltip(tr("Remove attachment"))
		x.Children(func() { widgets.Icon(c, "x", 14, widgets.Muted) })
		x.OnClick(remove)
	})
}

// completion is the composer's menu of slash commands and project files.
type completion struct {
	Input      conversation.InlineThreadInput
	Open       bool
	Choices    []model.ComposerChoice
	selected   int
	state      string
	Token      composer.ComposerToken
	Source     string
	cancel     context.CancelFunc
	generation int
}

func (m *completion) Close() {
	m.Open = false
	m.generation++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *completion) visible(app *App) bool {
	cur := app.C.Current()
	return m.Open && app.C.State().Draft() == m.Source && !cur.ReadOnly && !cur.Loading
}

// edit records an edit of the draft and opens the menu for the token at the
// caret: commands after a leading slash, files after an at sign.
func (m *completion) edit(app *App, value string) {
	m.editAt(app, value, composer.ComposerEditEnd(app.C.State().Draft(), value))
}

func (m *completion) editAt(app *App, value string, end int) {
	c := app.C
	c.RememberDraft(value)
	app.Notice = ""
	app.closeMenus()
	m.Close()
	m.Token = composer.ComposerTokenAt(value, end)
	if m.Token.Kind == '/' && m.Input.Doc != nil && m.Input.Doc.Text().Len() > 0 && m.Input.Doc.Text().AttributesAt(0).Tag == conversation.ComposerCommandTag {
		// Undo or a caret at the chip's edge edits an already selected command.
		m.Token = composer.ComposerToken{}
	}
	m.Source = value
	m.selected = 0
	m.Choices = nil
	if m.Token.Kind == 0 {
		return
	}
	m.Open = true
	if m.Token.Kind == '/' {
		m.Choices = app.CommandChoices(m.Token.Query)
		m.state = app.commandMenuState()
		return
	}
	if m.Token.Kind == '#' {
		if capability := c.CollaborationCapabilities(); capability == nil || capability.Version < 1 {
			m.Close()
			return
		}
		m.Choices = composer.AgentChoices(m.Token.Query, nil)
		return
	}
	m.state = tr("Searching project files…")
	// Agents to delegate to come first, then conversations of the workspace;
	// files follow as found.
	var refs []model.ComposerChoice
	if capability := c.CollaborationCapabilities(); capability != nil && capability.Version >= 1 {
		refs = composer.AgentChoices(m.Token.Query, nil)
	}
	refs = append(refs, app.ThreadChoices(m.Token.Query)...)
	m.Choices = refs
	ctx, stop := context.WithTimeout(c.State().Ctx, 4*time.Second)
	m.cancel = stop
	version, root, query := m.generation, c.State().Cwd, m.Token.Query
	var host *svcclient.Client
	if c.RemoteHost() {
		host = c.State().Remote
	}
	go func() {
		defer stop()
		timer := time.NewTimer(120 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		var files []string
		var err error
		if host == nil {
			files, err = projectfiles.IndexProjectFiles(ctx, root)
		} else {
			err = host.Call("host", svcproto.HostRequest{Action: "index", Project: root}, &files)
		}
		results := composer.FileChoices(files, query)
		c.Dispatch(func() {
			if m.generation != version || c.State().Cwd != root {
				return
			}
			if err != nil {
				m.state = tr("Unable to search files: ") + err.Error()
				return
			}
			m.Choices = append(append([]model.ComposerChoice(nil), refs...), results...)
			m.state = tr("No matching files")
		})
	}()
}

// choose applies a choice and returns the new draft.
func (m *completion) choose(app *App, index int) {
	c := app.C
	if !m.visible(app) || index < 0 || index >= len(m.Choices) {
		return
	}
	choice, token, value := m.Choices[index], m.Token, m.Source
	m.Close()
	if token.Kind == '#' {
		c.RememberDraft(composer.InsertAgentReference(value, token, choice.Agent, choice.Name))
		return
	}
	if token.Kind == '@' {
		if choice.Agent != "" {
			c.RememberDraft(composer.InsertAgentReference(value, token, choice.Agent, choice.Name))
			return
		}
		if choice.Ref != nil {
			c.RememberDraft(composer.InsertThreadReference(value, token, *choice.Ref, choice.Name))
			c.PrefetchReference(*choice.Ref)
			return
		}
		c.RememberDraft(composer.InsertFileReference(value, token, choice.Name))
		return
	}
	if choice.Name == "mention" {
		m.edit(app, "@")
		return
	}
	if !agent.IsHostCommand(choice.Name) {
		// Stage executable commands; selecting a menu item is not submission.
		c.RememberDraft("/" + choice.Name + " ")
		return
	}
	c.RememberDraft("/" + choice.Name)
	app.send()
}

func (app *App) composerInput(c *ui.Context) {
	cc := app.C
	cur := cc.Current()
	m := &app.Complete
	m.Input.Commands = app.knownCommandChoices("")
	placeholder := tr("Message… / Commands · @ Files")
	if cur.Busy {
		placeholder = tr("Enter to queue, ⌘Enter to steer now")
	}
	label := tr("Message ") + cc.CurrentAgentControls().Name()
	ui.Column(c).FillWidth().MinWidth(0).Gap(4).Children(func() {
		width := min(float32(744), max(200, c.Root().Bounds().W-340))
		in := app.ThreadInput(c.Key("composer"), &m.Input, cc.State().Draft(), fmt.Sprintf("%s:%d:%s", cc.State().Cwd, cur.ID, cc.CurrentAgent()), label, placeholder, 13, width, 2, 10, func(text string) {
			_, caret := m.Input.Doc.Selection()
			end := len(conversation.InlineThreadRaw(m.Input.Doc.Text().Slice(0, caret)))
			m.editAt(app, text, end)
		}).
			Disabled(cur.ReadOnly || cur.Loading).AutoFocus()
		if app.focusComposer && !cur.ReadOnly && !cur.Loading {
			in.Focus()
			app.focusComposer = false
		}
		if m.Input.Moved {
			m.Close()
		}
		visible := m.visible(app)
		if in.Shortcut(0, ui.KeyEnter) {
			if visible {
				m.choose(app, m.selected)
			} else if !cur.Loading || cur.Busy {
				app.send()
			}
			m.Input.SetRaw(cc.State().Draft())
		}
		if in.Shortcut(ui.Cmd, ui.KeyEnter) {
			if !visible && (!cur.Loading || cur.Busy) {
				app.SendDraft(cur.Busy)
				m.Input.SetRaw(cc.State().Draft())
			}
		}
		if visible {
			if in.Shortcut(0, ui.KeyTab) {
				m.choose(app, m.selected)
				m.Input.SetRaw(cc.State().Draft())
			}
			for _, key := range []ui.Key{ui.KeyUp, ui.KeyDown} {
				if in.Shortcut(0, key) && len(m.Choices) > 0 {
					step := 1
					if key == ui.KeyUp {
						step = -1
					}
					m.selected = (m.selected + step + len(m.Choices)) % len(m.Choices)
				}
			}
			if in.Shortcut(0, ui.KeyEscape) {
				m.Close()
			}
		}
		if in.Clicked() {
			m.Close()
		}
		if app.Notice != "" {
			ui.Text(c, app.Notice).FontSize(11).TextColor(widgets.Muted)
		}
		if hint := m.Input.CommandHint(); hint != "" {
			ui.Text(c, hint).FontSize(11).TextColor(widgets.Muted).Wrap().FillWidth()
		}
		open := m.visible(app)
		ui.PopoverBase(c, in, &open, func(panel ui.Element) {
			panel.AttachTo(in, ui.AnchorTopLeft, ui.AnchorBottomLeft).Margin(0, 0, 20, 0).
				Width(420).Padding(8).Gap(4).Background(widgets.Canvas).Border(1, widgets.Border).Radius(12).Shadow(0, 8, 24, 0, widgets.Shadow)
			title := tr("Commands")
			if m.Token.Kind == '#' {
				title = tr("Agents")
			}
			if m.Token.Kind == '@' {
				title = tr("Project files")
				for _, choice := range m.Choices {
					if choice.Agent != "" {
						title = tr("Agents, conversations and project files")
						break
					}
					if choice.Ref != nil {
						title = tr("Conversations and project files")
					}
				}
			}
			ui.Text(c, title).FontSize(11).TextColor(widgets.Muted).Padding(6)
			if len(m.Choices) == 0 {
				ui.Text(c, tr(m.state)).FontSize(12).TextColor(widgets.Muted).Padding(8)
			} else {
				start := m.selected / 6 * 6
				end := min(start+6, len(m.Choices))
				for i := start; i < end; i++ {
					item := m.Choices[i]
					text, detail := "/"+item.Name, item.Detail
					if m.Token.Kind == '#' {
						text = "#" + item.Name
					}
					if m.Token.Kind == '@' {
						text, detail = item.Name, ""
						if item.Agent != "" {
							text = "@" + item.Name
						}
						if item.Ref != nil || item.Agent != "" {
							detail = item.Detail
						}
					}
					key := item.Name
					if item.Agent != "" {
						key = item.Agent
					}
					if item.Ref != nil {
						key = item.Ref.URL()
					}
					row := widgets.Quiet(c.Key("choice:" + key)).FillWidth().Column().AlignItems(ui.Start).Height(0).MinHeight(40).Padding(8).Gap(2).Label(text)
					if i == m.selected {
						row.Background(widgets.SelectedBg)
					}
					row.Children(func() {
						ui.Text(c, text).FontSize(13)
						if detail != "" {
							ui.Text(c, detail).FontSize(11).TextColor(widgets.Muted)
						}
					})
					if row.Clicked() {
						m.choose(app, i)
					}
				}
			}
			ui.Text(c, fmt.Sprintf(tr("↑ ↓ select · Enter / Tab confirm · Esc close    %d results"), len(m.Choices))).FontSize(10).TextColor(widgets.Muted).Padding(6)
		})
		if !open && m.Open {
			m.Close()
		}
	})
}

// picker is a composer control that opens a panel of choices above it.
func (app *App) picker(c *ui.Context, open *bool, width float32, trigger func(), panel func()) {
	b := widgets.Quiet(c).PaddingX(4)
	b.Children(trigger)
	if b.Clicked() {
		*open = !*open
	}
	ui.PopoverBase(c, b, open, func(p ui.Element) {
		p.AttachTo(b, ui.AnchorTopLeft, ui.AnchorBottomLeft).Margin(0, 0, 8, 0).
			Width(width).Padding(14).Gap(10).Background(widgets.Canvas).Border(1, widgets.Border).Radius(12).Shadow(0, 8, 24, 0, widgets.Shadow)
		panel()
	})
}

func (app *App) choiceList(c *ui.Context, items []string, selected string, label func(string) string, pick func(string)) {
	ui.Column(c).Gap(3).Children(func() {
		for _, v := range items {
			b := widgets.Quiet(c.Key(v)).FillWidth()
			if v == selected {
				b.Background(widgets.Chosen)
			}
			b.Children(func() { ui.Text(c, label(v)) })
			if b.Clicked() {
				pick(v)
			}
		}
	})
}

func (app *App) modelPicker(c *ui.Context) {
	cc := app.C
	b := cc.CurrentAgentControls()
	app.picker(c.Key("model"), &app.modelOpen, 280, func() {
		ui.Text(c, b.ModelLabel(b.Model())).FontSize(11)
	}, func() {
		ui.Text(c, tr("Model")).FontWeight(600)
		app.choiceList(c, b.ModelChoices(), b.Model(), func(v string) string { return b.ModelLabel(v) }, func(v string) { b.SetModel(v); app.modelOpen = false })
	})
}

func (app *App) effortPicker(c *ui.Context) {
	cc := app.C
	b := cc.CurrentAgentControls()
	app.picker(c.Key("effort"), &app.effortOpen, 220, func() {
		ui.Text(c, tr(textutil.FirstNonEmpty(b.Effort(), "Default effort"))).TextColor(widgets.Muted).FontSize(11)
		widgets.Icon(c, "chevron-down", 12, widgets.Muted)
	}, func() {
		ui.Text(c, tr("Thinking")).FontWeight(600)
		app.choiceList(c, b.EffortChoices(), b.Effort(), func(v string) string { return tr(textutil.FirstNonEmpty(v, "Default")) }, func(v string) { b.SetEffort(v); app.effortOpen = false })
	})
}

func (app *App) agentPicker(c *ui.Context) {
	cc := app.C
	agent := cc.CurrentAgent()
	name := model.AgentName(agent)
	c = c.Key("agent")
	// The agent is fixed once a conversation has its first message.
	b := widgets.Quiet(c).PaddingX(4).Disabled(cc.Current().ID != 0)
	b.Children(func() {
		widgets.AgentIcon(c, agent, 18)
		ui.Text(c, name)
		if speed := cc.AgentSpeedBadge(agent); speed != "" {
			ui.Text(c, speed).FontSize(10).FontWeight(600).TextColor(widgets.Muted)
		}
		if cc.Current().ID == 0 {
			widgets.Icon(c, "chevron-down", 12, widgets.Muted)
		}
	})
	if b.Clicked() {
		app.agentOpen = !app.agentOpen
	}
	ui.PopoverBase(c, b, &app.agentOpen, func(p ui.Element) {
		p.AttachTo(b, ui.AnchorTopLeft, ui.AnchorBottomLeft).Margin(0, 0, 8, 0).
			Width(220).Padding(6).Background(widgets.Canvas).Border(1, widgets.Line).Radius(10).Shadow(0, 8, 24, 0, widgets.Shadow)
		for _, agent := range model.Backends {
			choice := struct{ id, name string }{agent, model.AgentName(agent)}
			item := widgets.Quiet(c.Key(choice.id)).FillWidth()
			if choice.id == agent {
				item.Background(widgets.Chosen)
			}
			item.Children(func() {
				widgets.AgentIcon(c, choice.id, 18)
				ui.Text(c, choice.name)
			})
			if item.Clicked() {
				cc.State().SetAgent(choice.id)
				app.agentOpen = false
			}
		}
	})
}

func (app *App) permissionsPicker(c *ui.Context) {
	cc := app.C
	c = c.Key("permissions")
	selected := cc.CurrentPermissionPolicy()
	choice := permissionChoiceFor(selected)
	b := widgets.Quiet(c).Width(32).Justify(ui.Center).PaddingX(0).Label(choice.title).Tooltip(choice.title)
	b.Children(func() {
		widgets.Icon(c, choice.icon, 15, choice.color)
	})
	if b.Clicked() {
		app.permissionsOpen = !app.permissionsOpen
		app.modelOpen, app.effortOpen = false, false
	}
	ui.PopoverBase(c, b, &app.permissionsOpen, func(p ui.Element) {
		p.AttachTo(b, ui.AnchorTopRight, ui.AnchorBottomRight).Margin(0, 0, 10, 0).
			Width(390).Padding(16).Gap(10).Background(widgets.Canvas).Border(1, widgets.Border).Radius(12).Shadow(0, 8, 24, 0, widgets.Shadow)
		title := cc.CurrentAgentControls().Name() + tr(" permissions")
		ui.Text(c, title).FontWeight(600)
		permissionOptions(c, selected, func(v string) {
			cc.SetCurrentPermissionPolicy(v)
			app.permissionsOpen = false
		})
	})
}

type permissionChoice struct {
	value, title, detail, icon string
	color                      ui.Color
}

func permissionChoices() []permissionChoice {
	return []permissionChoice{
		{value: "on-request", title: tr("Ask when needed"), detail: tr("Always ask before editing external files or accessing the internet"), icon: "hand-stop", color: widgets.Muted},
		{value: "on-failure", title: tr("Automatic approval"), detail: tr("Request approval only for detected risky actions"), icon: "shield-check", color: widgets.Muted},
		{value: "never", title: tr("Full access"), detail: tr("Unrestricted access to the internet and any file on your computer"), icon: "shield-exclamation", color: ui.Hex("#db6b3d")},
	}
}

func permissionChoiceFor(policy string) permissionChoice {
	for _, choice := range permissionChoices() {
		if choice.value == policy {
			return choice
		}
	}
	return permissionChoices()[0]
}

func permissionChoiceTitle(policy string) string { return permissionChoiceFor(policy).title }

func permissionOptions(c *ui.Context, selected string, pick func(string)) {
	ui.Column(c).Gap(3).Children(func() {
		for _, choice := range permissionChoices() {
			b := widgets.Quiet(c.Key(choice.value)).FillWidth().Height(0).MinHeight(52).Padding(8).Gap(10)
			if choice.value == selected {
				b.Background(widgets.SelectedBg)
			}
			b.Children(func() {
				widgets.Icon(c, choice.icon, 22, choice.color)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, choice.title).FontWeight(600).TextColor(choice.color).FontSize(13)
					ui.Text(c, choice.detail).FontSize(11).TextColor(choice.color)
				})
				if choice.value == selected {
					ui.Text(c, "✓").FontSize(18).TextColor(widgets.Muted)
				}
			})
			if b.Clicked() {
				pick(choice.value)
			}
		}
	})
}
