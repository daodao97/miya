package main

import (
	"context"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/textutil"
)

func (app *chatApp) composerArea(c *ui.Context) {
	cc := app.c
	cur := cc.current()
	ui.Row(c).FillWidth().Justify(ui.Center).Shrink(0).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(800).Padding(0, 28, 10, 28).Gap(10).Children(func() {
			if cur.ID == 0 {
				ui.Text(c, tr("A few ideas to get started")).AlignSelf(ui.End).FontSize(11).TextColor(muted)
				ui.Row(c).Gap(10).Children(func() {
					app.suggestion(c.Key("snake"), "device-gamepad-2", tr("Create a classic snake game"), tr("Create a classic snake game in this project."))
					app.suggestion(c.Key("bug"), "bug", tr("Find and fix a bug in my code"), tr("Find and fix a bug in my code. Explain the root cause and verify the fix."))
					app.suggestion(c.Key("pdf"), "file-text", tr("Summarize this app in a PDF"), tr("Summarize this app in a one-page PDF."))
				})
			}
			msg, reconnect := cc.currentBackend().Problem(cc)
			if msg != "" || len(cc.approvals()) > 0 {
				ui.Column(c).Gap(8).Children(func() {
					if msg != "" {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							mutedText(c, msg).Grow(1).MinWidth(0)
							if reconnect && quiet(c.Key("reconnect")).Children(func() { ui.Text(c, tr("Reconnect")) }).Clicked() {
								cc.currentBackend().Reconnect(cc)
							}
						})
					}
					for _, a := range cc.approvals() {
						app.approvalView(c.Key("approval:"+string(a.ID)), a)
					}
				})
			}
			app.queueView(c, cur)
			app.taskStatus(c, cur)
			app.collaborationTasks(c, cur)
			if cur.ReadOnly {
				ui.Row(c).AlignItems(ui.Center).Padding(18).Gap(12).Border(1, line).Radius(16).Children(func() {
					icon(c, "lock", 18, muted)
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, tr("Open in another app")).FontWeight(600)
						mutedText(c, tr("Close the conversation there before continuing here."))
					})
					if quiet(c.Key("retry-readonly")).Disabled(cur.Loading).Children(func() { ui.Text(c, tr("Retry")) }).Clicked() {
						cc.resume(cur, nil)
					}
				})
				return
			}
			app.composerBox(c)
		})
	})
}

func (app *chatApp) suggestion(c *ui.Context, symbol, text, prompt string) {
	b := quiet(c).Column().AlignItems(ui.Start).Justify(ui.SpaceBetween).Grow(1).Basis(0).MinWidth(0).Height(82).Padding(14).
		Background(subtle).Radius(14)
	if b.Hovered() {
		b.Background(hover)
	}
	b.Children(func() {
		icon(c, symbol, 20, faint)
		ui.Text(c, text).FontSize(11)
	})
	if b.Clicked() {
		app.c.rememberDraft(prompt)
	}
}

func (app *chatApp) composerBox(c *ui.Context) {
	cc := app.c
	cur := cc.current()
	ui.Column(c).Background(panel).Border(1, line).Radius(16).Padding(12).Gap(10).Children(func() {
		if paths := cc.attachments(); len(paths) > 0 {
			ui.Row(c).Wrap().Gap(8).FillWidth().MinWidth(0).Children(func() {
				for _, p := range paths {
					app.attachmentPreview(c.Key(p), p)
				}
			})
		}
		app.composerInput(c)
		ui.Row(c).AlignItems(ui.Center).Gap(2).Children(func() {
			if toolButton(c.Key("attach"), "plus", tr("Add context")).Clicked() {
				app.attachFiles()
			}
			app.agentPicker(c)
			ui.Spacer(c)
			app.permissionsPicker(c)
			caps := cc.currentBackend().Caps()
			if caps.Models {
				app.modelPicker(c)
			}
			if caps.Effort {
				app.effortPicker(c)
			}
			hasDraft := strings.TrimSpace(cc.draft()) != "" || len(cc.attachments()) > 0
			stopping := cur.Busy && !hasDraft
			label, symbol := tr("Send message"), "arrow-up"
			switch {
			case stopping:
				label, symbol = tr("Stop response"), "x"
			case cur.Busy:
				label = tr("Add to queue")
			}
			disabled := cur.ReadOnly || cur.TaskPending || !cc.backendReady() || (!cur.Busy && (cur.Loading || !hasDraft))
			if cur.Busy && hasDraft {
				q := quiet(c.Key("steer-draft")).Label(tr("Steer now")).Tooltip(tr("Insert message into the current turn · ⌘Enter"))
				q.Children(func() {
					icon(c, "arrow-up", 14, muted)
					ui.Text(c, tr("Steer now"))
				})
				if q.Clicked() {
					app.sendDraft(true)
				}
			}
			send := ui.ButtonBase(c.Key("send")).Size(30, 30).Radius(15).Justify(ui.Center).AlignItems(ui.Center).
				Background(chosen).Label(label).Tooltip(label).Disabled(disabled)
			if disabled {
				send.Opacity(0.35)
			} else if send.Hovered() {
				send.Background(sendHover)
			}
			send.Children(func() { icon(c, symbol, 18, ink) })
			if send.Clicked() {
				if stopping {
					cc.stop()
				} else {
					app.send()
				}
			}
		})
	})
}

func (app *chatApp) attachmentPreview(c *ui.Context, path string) {
	remove := func() { app.c.removeAttachment(path) }
	name := filepath.Base(path)
	if strings.HasPrefix(mime.TypeByExtension(filepath.Ext(path)), "image/") {
		ui.Box(c).Size(160, 120).Shrink(0).Children(func() {
			var img ui.ImageSource
			if strings.HasPrefix(path, "miya-server:") {
				img = app.remoteMessageImage(serverAttachmentSource(path))
			} else {
				img = cachedImage(path)
			}
			if img != nil {
				ui.Image(c, img).Size(160, 120).Fit(ui.Cover).Radius(8)
			} else {
				ui.Box(c).Size(160, 120).Radius(8).Background(bubble)
			}
			x := ui.ButtonBase(c.Key("remove")).Absolute().Top(-3).Right(-3).Size(20, 20).Radius(10).Justify(ui.Center).AlignItems(ui.Center).
				Background(ui.Hex("#303030")).Border(2, canvas).Label(tr("Remove attachment ") + name).Tooltip(tr("Remove attachment"))
			if x.Hovered() {
				x.Background(ui.Hex("#505050"))
			}
			x.Children(func() { icon(c, "x", 12, canvas) })
			x.OnClick(remove)
		})
		return
	}
	ui.Row(c).AlignItems(ui.Center).Gap(4).MinWidth(0).Padding(4).Border(1, line).Radius(8).Children(func() {
		ui.Text(c, name).MaxWidth(200).SingleLine().Ellipsis("…").TextColor(muted).FontSize(11)
		x := quiet(c.Key("remove")).Size(24, 24).PaddingX(3).Justify(ui.Center).Label(tr("Remove attachment ") + name).Tooltip(tr("Remove attachment"))
		x.Children(func() { icon(c, "x", 14, muted) })
		x.OnClick(remove)
	})
}

// completion is the composer's menu of slash commands and project files.
type completion struct {
	input      inlineThreadInput
	open       bool
	choices    []composerChoice
	selected   int
	state      string
	token      composerToken
	source     string
	cancel     context.CancelFunc
	generation int
}

func (m *completion) close() {
	m.open = false
	m.generation++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *completion) visible(app *chatApp) bool {
	cur := app.c.current()
	return m.open && app.c.draft() == m.source && !cur.ReadOnly && !cur.Loading
}

// edit records an edit of the draft and opens the menu for the token at the
// caret: commands after a leading slash, files after an at sign.
func (m *completion) edit(app *chatApp, value string) {
	m.editAt(app, value, composerEditEnd(app.c.draft(), value))
}

func (m *completion) editAt(app *chatApp, value string, end int) {
	c := app.c
	c.rememberDraft(value)
	app.notice = ""
	app.closeMenus()
	m.close()
	m.token = composerTokenAt(value, end)
	if m.token.Kind == '/' && m.input.doc != nil && m.input.doc.Text().Len() > 0 && m.input.doc.Text().AttributesAt(0).Tag == composerCommandTag {
		// Undo or a caret at the chip's edge edits an already selected command.
		m.token = composerToken{}
	}
	m.source = value
	m.selected = 0
	m.choices = nil
	if m.token.Kind == 0 {
		return
	}
	m.open = true
	if m.token.Kind == '/' {
		m.choices = app.commandChoices(m.token.Query)
		m.state = app.commandMenuState()
		return
	}
	if m.token.Kind == '#' {
		if capability := c.collaborationCapabilities(); capability == nil || capability.Version < 1 {
			m.close()
			return
		}
		m.choices = agentChoices(m.token.Query, nil)
		return
	}
	m.state = tr("Searching project files…")
	// Agents to delegate to come first, then conversations of the workspace;
	// files follow as found.
	var refs []composerChoice
	if capability := c.collaborationCapabilities(); capability != nil && capability.Version >= 1 {
		refs = agentChoices(m.token.Query, nil)
	}
	refs = append(refs, app.threadChoices(m.token.Query)...)
	m.choices = refs
	ctx, stop := context.WithTimeout(c.ctx, 4*time.Second)
	m.cancel = stop
	version, root, query := m.generation, c.cwd, m.token.Query
	var host *serviceClient
	if c.remoteHost() {
		host = c.remote
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
			files, err = indexProjectFiles(ctx, root)
		} else {
			err = host.call("host", hostRequest{Action: "index", Project: root}, &files)
		}
		results := fileChoices(files, query)
		c.dispatch(func() {
			if m.generation != version || c.cwd != root {
				return
			}
			if err != nil {
				m.state = tr("Unable to search files: ") + err.Error()
				return
			}
			m.choices = append(append([]composerChoice(nil), refs...), results...)
			m.state = tr("No matching files")
		})
	}()
}

// choose applies a choice and returns the new draft.
func (m *completion) choose(app *chatApp, index int) {
	c := app.c
	if !m.visible(app) || index < 0 || index >= len(m.choices) {
		return
	}
	choice, token, value := m.choices[index], m.token, m.source
	m.close()
	if token.Kind == '#' {
		c.rememberDraft(insertAgentReference(value, token, choice.Agent, choice.Name))
		return
	}
	if token.Kind == '@' {
		if choice.Agent != "" {
			c.rememberDraft(insertAgentReference(value, token, choice.Agent, choice.Name))
			return
		}
		if choice.Ref != nil {
			c.rememberDraft(insertThreadReference(value, token, *choice.Ref, choice.Name))
			c.prefetchReference(*choice.Ref)
			return
		}
		c.rememberDraft(insertFileReference(value, token, choice.Name))
		return
	}
	if choice.Name == "mention" {
		m.edit(app, "@")
		return
	}
	if !appUICommands[choice.Name] {
		// Stage executable commands; selecting a menu item is not submission.
		c.rememberDraft("/" + choice.Name + " ")
		return
	}
	c.rememberDraft("/" + choice.Name)
	app.send()
}

func (app *chatApp) composerInput(c *ui.Context) {
	cc := app.c
	cur := cc.current()
	m := &app.complete
	m.input.commands = app.knownCommandChoices("")
	placeholder := tr("Message… / Commands · @ Files")
	if cur.Busy {
		placeholder = tr("Enter to queue, ⌘Enter to steer now")
	}
	label := tr("Message ") + cc.currentBackend().Name()
	ui.Column(c).FillWidth().MinWidth(0).Gap(4).Children(func() {
		width := min(float32(744), max(200, c.Root().Bounds().W-340))
		in := app.threadInput(c.Key("composer"), &m.input, cc.draft(), fmt.Sprintf("%s:%d:%s", cc.cwd, cur.ID, cc.currentAgent()), label, placeholder, 13, width, 2, 10, func(text string) {
			_, caret := m.input.doc.Selection()
			end := len(inlineThreadRaw(m.input.doc.Text().Slice(0, caret)))
			m.editAt(app, text, end)
		}).
			Disabled(cur.ReadOnly || cur.Loading).AutoFocus()
		if app.focusComposer && !cur.ReadOnly && !cur.Loading {
			in.Focus()
			app.focusComposer = false
		}
		if m.input.moved {
			m.close()
		}
		visible := m.visible(app)
		if in.Shortcut(0, ui.KeyEnter) {
			if visible {
				m.choose(app, m.selected)
			} else if !cur.Loading || cur.Busy {
				app.send()
			}
			m.input.setRaw(cc.draft())
		}
		if in.Shortcut(ui.Cmd, ui.KeyEnter) {
			if !visible && (!cur.Loading || cur.Busy) {
				app.sendDraft(cur.Busy)
				m.input.setRaw(cc.draft())
			}
		}
		if visible {
			if in.Shortcut(0, ui.KeyTab) {
				m.choose(app, m.selected)
				m.input.setRaw(cc.draft())
			}
			for _, key := range []ui.Key{ui.KeyUp, ui.KeyDown} {
				if in.Shortcut(0, key) && len(m.choices) > 0 {
					step := 1
					if key == ui.KeyUp {
						step = -1
					}
					m.selected = (m.selected + step + len(m.choices)) % len(m.choices)
				}
			}
			if in.Shortcut(0, ui.KeyEscape) {
				m.close()
			}
		}
		if in.Clicked() {
			m.close()
		}
		if app.notice != "" {
			ui.Text(c, app.notice).FontSize(11).TextColor(muted)
		}
		if hint := m.input.commandHint(); hint != "" {
			ui.Text(c, hint).FontSize(11).TextColor(muted).Wrap().FillWidth()
		}
		open := m.visible(app)
		ui.PopoverBase(c, in, &open, func(panel ui.Element) {
			panel.AttachTo(in, ui.AnchorTopLeft, ui.AnchorBottomLeft).Margin(0, 0, 20, 0).
				Width(420).Padding(8).Gap(4).Background(canvas).Border(1, border).Radius(12).Shadow(0, 8, 24, 0, shadow)
			title := tr("Commands")
			if m.token.Kind == '#' {
				title = tr("Agents")
			}
			if m.token.Kind == '@' {
				title = tr("Project files")
				for _, choice := range m.choices {
					if choice.Agent != "" {
						title = tr("Agents, conversations and project files")
						break
					}
					if choice.Ref != nil {
						title = tr("Conversations and project files")
					}
				}
			}
			ui.Text(c, title).FontSize(11).TextColor(muted).Padding(6)
			if len(m.choices) == 0 {
				ui.Text(c, tr(m.state)).FontSize(12).TextColor(muted).Padding(8)
			} else {
				start := m.selected / 6 * 6
				end := min(start+6, len(m.choices))
				for i := start; i < end; i++ {
					item := m.choices[i]
					text, detail := "/"+item.Name, item.Detail
					if m.token.Kind == '#' {
						text = "#" + item.Name
					}
					if m.token.Kind == '@' {
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
					row := quiet(c.Key("choice:" + key)).FillWidth().Column().AlignItems(ui.Start).Height(0).MinHeight(40).Padding(8).Gap(2).Label(text)
					if i == m.selected {
						row.Background(selectedBg)
					}
					row.Children(func() {
						ui.Text(c, text).FontSize(13)
						if detail != "" {
							ui.Text(c, detail).FontSize(11).TextColor(muted)
						}
					})
					if row.Clicked() {
						m.choose(app, i)
					}
				}
			}
			ui.Text(c, fmt.Sprintf(tr("↑ ↓ select · Enter / Tab confirm · Esc close    %d results"), len(m.choices))).FontSize(10).TextColor(muted).Padding(6)
		})
		if !open && m.open {
			m.close()
		}
	})
}

// picker is a composer control that opens a panel of choices above it.
func (app *chatApp) picker(c *ui.Context, open *bool, width float32, trigger func(), panel func()) {
	b := quiet(c).PaddingX(4)
	b.Children(trigger)
	if b.Clicked() {
		*open = !*open
	}
	ui.PopoverBase(c, b, open, func(p ui.Element) {
		p.AttachTo(b, ui.AnchorTopLeft, ui.AnchorBottomLeft).Margin(0, 0, 8, 0).
			Width(width).Padding(14).Gap(10).Background(canvas).Border(1, border).Radius(12).Shadow(0, 8, 24, 0, shadow)
		panel()
	})
}

func (app *chatApp) choiceList(c *ui.Context, items []string, selected string, label func(string) string, pick func(string)) {
	ui.Column(c).Gap(3).Children(func() {
		for _, v := range items {
			b := quiet(c.Key(v)).FillWidth()
			if v == selected {
				b.Background(chosen)
			}
			b.Children(func() { ui.Text(c, label(v)) })
			if b.Clicked() {
				pick(v)
			}
		}
	})
}

func (app *chatApp) modelPicker(c *ui.Context) {
	cc := app.c
	b := cc.currentBackend()
	app.picker(c.Key("model"), &app.modelOpen, 280, func() {
		ui.Text(c, b.ModelLabel(cc, b.Model(cc))).FontSize(11)
	}, func() {
		ui.Text(c, tr("Model")).FontWeight(600)
		app.choiceList(c, b.ModelChoices(cc), b.Model(cc), func(v string) string { return b.ModelLabel(cc, v) }, func(v string) { b.SetModel(cc, v); app.modelOpen = false })
	})
}

func (app *chatApp) effortPicker(c *ui.Context) {
	cc := app.c
	b := cc.currentBackend()
	app.picker(c.Key("effort"), &app.effortOpen, 220, func() {
		ui.Text(c, tr(textutil.FirstNonEmpty(b.Effort(cc), "Default effort"))).TextColor(muted).FontSize(11)
		icon(c, "chevron-down", 12, muted)
	}, func() {
		ui.Text(c, tr("Thinking")).FontWeight(600)
		app.choiceList(c, b.EffortChoices(cc), b.Effort(cc), func(v string) string { return tr(textutil.FirstNonEmpty(v, "Default")) }, func(v string) { b.SetEffort(cc, v); app.effortOpen = false })
	})
}

func (app *chatApp) agentPicker(c *ui.Context) {
	cc := app.c
	agent := cc.currentAgent()
	name := backendFor(agent).Name()
	c = c.Key("agent")
	// The agent is fixed once a conversation has its first message.
	b := quiet(c).PaddingX(4).Disabled(cc.current().ID != 0)
	b.Children(func() {
		agentIcon(c, agent, 18)
		ui.Text(c, name)
		if speed := cc.agentSpeedBadge(agent); speed != "" {
			ui.Text(c, speed).FontSize(10).FontWeight(600).TextColor(muted)
		}
		if cc.current().ID == 0 {
			icon(c, "chevron-down", 12, muted)
		}
	})
	if b.Clicked() {
		app.agentOpen = !app.agentOpen
	}
	ui.PopoverBase(c, b, &app.agentOpen, func(p ui.Element) {
		p.AttachTo(b, ui.AnchorTopLeft, ui.AnchorBottomLeft).Margin(0, 0, 8, 0).
			Width(220).Padding(6).Background(canvas).Border(1, line).Radius(10).Shadow(0, 8, 24, 0, shadow)
		for _, b := range agentBackends {
			choice := struct{ id, name string }{b.ID(), b.Name()}
			item := quiet(c.Key(choice.id)).FillWidth()
			if choice.id == agent {
				item.Background(chosen)
			}
			item.Children(func() {
				agentIcon(c, choice.id, 18)
				ui.Text(c, choice.name)
			})
			if item.Clicked() {
				cc.setAgent(choice.id)
				app.agentOpen = false
			}
		}
	})
}

func (app *chatApp) permissionsPicker(c *ui.Context) {
	cc := app.c
	c = c.Key("permissions")
	selected := cc.currentPermissionPolicy()
	choice := permissionChoiceFor(selected)
	b := quiet(c).Width(32).Justify(ui.Center).PaddingX(0).Label(choice.title).Tooltip(choice.title)
	b.Children(func() {
		icon(c, choice.icon, 15, choice.color)
	})
	if b.Clicked() {
		app.permissionsOpen = !app.permissionsOpen
		app.modelOpen, app.effortOpen = false, false
	}
	ui.PopoverBase(c, b, &app.permissionsOpen, func(p ui.Element) {
		p.AttachTo(b, ui.AnchorTopRight, ui.AnchorBottomRight).Margin(0, 0, 10, 0).
			Width(390).Padding(16).Gap(10).Background(canvas).Border(1, border).Radius(12).Shadow(0, 8, 24, 0, shadow)
		title := cc.currentBackend().Name() + tr(" permissions")
		ui.Text(c, title).FontWeight(600)
		permissionOptions(c, selected, func(v string) {
			cc.setCurrentPermissionPolicy(v)
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
		{value: "on-request", title: tr("Ask when needed"), detail: tr("Always ask before editing external files or accessing the internet"), icon: "hand-stop", color: muted},
		{value: "on-failure", title: tr("Automatic approval"), detail: tr("Request approval only for detected risky actions"), icon: "shield-check", color: muted},
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
			b := quiet(c.Key(choice.value)).FillWidth().Height(0).MinHeight(52).Padding(8).Gap(10)
			if choice.value == selected {
				b.Background(selectedBg)
			}
			b.Children(func() {
				icon(c, choice.icon, 22, choice.color)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, choice.title).FontWeight(600).TextColor(choice.color).FontSize(13)
					ui.Text(c, choice.detail).FontSize(11).TextColor(choice.color)
				})
				if choice.value == selected {
					ui.Text(c, "✓").FontSize(18).TextColor(muted)
				}
			})
			if b.Clicked() {
				pick(choice.value)
			}
		}
	})
}
