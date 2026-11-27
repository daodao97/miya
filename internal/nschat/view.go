// Package nschat is the conversation view on NSGo, shared by the desktop and
// the phone. Grouping and presentation stay
// in internal/chat/presentation; this package only draws them.
package nschat

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base/diff"
	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/ui/chat"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/chat/subagent"
	"miya/internal/nswidgets"
	"miya/locales"
)

func tr(english string) string { return locales.T(english) }

// View shows a conversation. Thread must be a tracked getter (nsui.Live
// wraps the controller's): the view follows every change of it.
type View struct {
	Thread func() model.Thread
	// OnRetry asks for a failed response again; OnReload loads a
	// conversation that failed to load.
	OnRetry  func(appmodel.RetryRequest)
	OnReload func()
	// OnHandoff starts a new conversation from this one's latest response.
	OnHandoff func()
	// Draft shows the composer: the conversation's draft, which
	// OnDraftChange keeps. OnSend sends it, OnStop stops the running turn.
	// Without Draft the view has no composer, as for a read-only mirror.
	Draft         func() string
	OnDraftChange func(string)
	OnSend        func()
	OnStop        func()
	SendDisabled  func() bool
	// Composer overrides the default composer for surfaces with phone controls.
	Composer core.Node
	// OnLink handles thread and desktop-file references in the owning surface.
	OnLink func(string)
	// Requests are the pending approvals and questions, above the composer.
	Requests core.Node
	// Above contains app-owned status, suggestions and notices. Accessory and
	// Trailing are composer controls; Attachments is the removable preview row.
	Above, Accessory, Trailing, Attachments core.Node
	HasAttachments                          func() bool
	ComposerVisible                         func() bool
	EditorDisabled                          func() bool
	EditorReadonly                          func() bool
	// Commands and FileReferences identify confirmed inline tokens.
	Commands       func() []model.ComposerChoice
	FileReferences func() []string
	// CaptureKeys/OnComposerKey consume completion keys inside the editor.
	CaptureKeys        func() []string
	OnComposerKey      func(string)
	OnComposerShortcut func(string) bool
	WaitingApproval    func() bool
	// OnDraftEdit includes the UTF-8 wire offset of the caret.
	OnDraftEdit        func(string, int)
	OnComposerMove     func()
	OnInvalidReference func()
	// ComposerOwner resets undo when the workspace or conversation changes.
	ComposerOwner func() string
	OnDropFiles   func([]string)
	OnPaste       func(core.PasteEvent)
	OnSteer       func()
	// UserContent can replace a prompt with an app-owned editing surface.
	UserContent func(*core.Scope, func() model.Message, core.Node) core.Node
	// ImageSource resolves remote thumbnails without coupling this view to a host.
	ImageSource     func(*core.Scope, string) func() string
	ReferenceTitle  func(model.ThreadRef) string
	OnOpenReference func(model.ThreadRef)
	State           *chat.ConversationState
	TestID          string
}

// maxWidth is the column the conversation keeps to on wide windows.
const maxWidth = 744

// Render implements core.Node.
func (v View) Render(cx *core.Scope) core.Node {
	if v.State == nil {
		v.State = chat.NewConversationState(cx)
	}

	clockStart, wallStart := cx.Now(), time.Now()
	now := core.NewSignal(cx, wallStart)
	cx.Effect(func() {
		if !v.Thread().Busy {
			return
		}
		var stop func() bool
		var tick func()
		tick = func() { now.Set(wallStart.Add(cx.Now().Sub(clockStart))); stop = cx.AfterFunc(time.Second, tick) }
		stop = cx.AfterFunc(time.Second, tick)
		cx.Cleanup(func() { stop() })
	})
	groups := func() []msgfmt.MessageGroup {
		t := v.Thread()
		return msgfmt.ThreadMessageGroups(t, msgfmt.GroupMessages(t.Messages))
	}
	// What shows when there are no groups: nothing, loading, or a failure.
	empty := core.NewMemo(cx, func() string {
		t := v.Thread()
		switch {
		case len(groups()) > 0:
			return ""
		case len(t.Messages) == 0 && !t.Loading && t.Status != "" && !t.Busy:
			return "failed"
		case t.Loading && len(t.Messages) == 0:
			return "loading"
		case len(t.Messages) > 0:
			return "hidden"
		}
		return ""
	})
	column := func(children ...core.Node) core.Node {
		return core.View{Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(maxWidth), AlignSelf: core.AlignCenter, Gap: 28}, Children: children}
	}
	// The status lines and the composer float over the transcript's end,
	// as Agio's do: the messages scroll behind them.
	floating := []core.Node{
		core.View{TestID: "turn-status", StyleFunc: func() core.Style {
			return core.Style{Display: displayIf(turnStatus(v.Thread(), groups(), now.Get()) != ""), Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, MinHeight: core.Pt(28), Padding: core.Symmetric(0, 4)}
		},
			Children: []core.Node{nswidgets.WorkingLabel(cx, "clock", func() string { return turnStatus(v.Thread(), groups(), now.Get()) }), v.diffSummary(cx, groups)}},

		core.View{StyleFunc: func() core.Style {
			return core.Style{Display: displayIf(len(v.Thread().BackgroundTasks) > 0), Padding: core.Symmetric(0, 4)}
		}, Children: []core.Node{nswidgets.MutedText(cx, func() string { return backgroundTasks(v.Thread()) })}},
	}

	floating = append(floating, core.View{StyleFunc: func() core.Style {
		return core.Style{Display: displayIf(v.stalledHint(now.Get()) != "")}
	}, Children: []core.Node{nswidgets.MutedText(cx, func() string { return v.stalledHint(now.Get()) })}})
	if v.Above != nil {
		floating = append(floating, v.Above)
	}
	if v.Requests != nil {
		floating = append(floating, v.Requests)
	}
	if v.Composer != nil {
		floating = append(floating, v.Composer)
	} else if v.Draft != nil {
		floating = append(floating, core.Show{When: func() bool { return v.ComposerVisible == nil || v.ComposerVisible() }, Then: func(cx *core.Scope) core.Node { return v.composer(cx) }})
	}
	transcript := column(
		core.Each(groups, func(g msgfmt.MessageGroup) int { return g.ID },
			func(cx *core.Scope, group func() msgfmt.MessageGroup, _ func() int) core.Node {
				return v.groupRow(cx, group)
			}),
		core.Dynamic(func(cx *core.Scope) core.Node { return v.emptyState(cx, empty.Get()) }),
	)
	// On a desktop the composer docks under the transcript, as Miya's window
	// always had it: the status lines read against the window and no message
	// shows through between them. A phone floats it on glass.
	if cx.Env().Desktop() {
		return core.View{TestID: v.TestID, Style: ui.FillRest, Children: []core.Node{
			chat.Conversation{State: v.State, JumpLabel: tr("↓ Latest"), TestID: "chat-transcript", Children: []core.Node{transcript}},
			core.View{Style: core.Style{FlexShrink: 0, AlignItems: core.AlignCenter, Padding: core.Edges{Right: 20, Bottom: 12, Left: 20}}, Children: []core.Node{
				core.View{TestID: "composer-dock", Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(maxWidth), MinWidth: core.Pt(0)}, Children: []core.Node{nswidgets.Stack(cx, 8, floating...)}},
			}},
		}}
	}
	return chat.FloatingConversation{State: v.State, JumpLabel: tr("↓ Latest"), TestID: v.TestID,
		Children: []core.Node{transcript},
		Composer: []core.Node{nswidgets.Stack(cx, 6, floating...)},
	}
}

func (v View) diffSummary(cx *core.Scope, groups func() []msgfmt.MessageGroup) core.Node {
	return core.View{TestID: "turn-diff", StyleFunc: func() core.Style {
		stats := latestDiff(groups())
		return core.Style{Display: displayIf(!v.Thread().Busy && stats != (model.DiffLineStats{})), Direction: core.Row, Gap: 8}
	}, Children: []core.Node{
		core.Show{When: func() bool { return latestDiff(groups()).Added > 0 }, Then: func(cx *core.Scope) core.Node {
			return core.Text{TestID: "turn-added", ContentFunc: func() string { return "+" + strconv.Itoa(latestDiff(groups()).Added) }, Style: core.Style{FontSize: 12}, StyleFunc: func() core.Style { return core.Style{Color: nswidgets.ColorsOf(cx).Ink()} }}
		}},
		core.Show{When: func() bool { return latestDiff(groups()).Deleted > 0 }, Then: func(cx *core.Scope) core.Node {
			return core.Text{TestID: "turn-deleted", ContentFunc: func() string { return "−" + strconv.Itoa(latestDiff(groups()).Deleted) }, Style: core.Style{FontSize: 12}, StyleFunc: func() core.Style { return core.Style{Color: nswidgets.ColorsOf(cx).Danger()} }}
		}},
	}}
}

// Only the latest response contributes to the floating turn summary.
func latestDiff(groups []msgfmt.MessageGroup) model.DiffLineStats {
	for i := len(groups) - 1; i >= 0; i-- {
		if !groups[i].User {
			return groups[i].DiffStats
		}
	}
	return model.DiffLineStats{}
}

func displayIf(on bool) core.Display {
	if on {
		return core.DisplayFlex
	}
	return core.DisplayNone
}

// turnStatus is the label above the composer: the running turn's progress,
// or how long the latest completed response took.
func turnStatus(t model.Thread, groups []msgfmt.MessageGroup, now time.Time) string {
	if t.Busy {
		if label := msgfmt.ThreadReconnectLabel(t); label != "" {
			return label
		}
		return msgfmt.ThreadWorkingLabelAt(t, now)
	}
	if last := len(groups) - 1; last >= 0 && !groups[last].User && len(t.BackgroundTasks) == 0 {
		return msgfmt.TurnElapsedLabel(groups[last].Messages, false)
	}
	return ""
}

func (v View) stalledHint(now time.Time) string {
	return appmodel.StalledHint(v.Thread(), v.WaitingApproval != nil && v.WaitingApproval(), now)
}

func backgroundTasks(t model.Thread) string {
	if len(t.BackgroundTasks) == 0 {
		return ""
	}
	names := make([]string, 0, len(t.BackgroundTasks))
	for _, task := range t.BackgroundTasks {
		names = append(names, strings.Join(strings.Fields(task.Description), " "))
	}
	return strconv.Itoa(len(t.BackgroundTasks)) + " · " + strings.Join(names, " · ")
}

func (v View) emptyState(cx *core.Scope, state string) core.Node {
	switch state {
	case "failed":
		return core.View{Style: core.Style{AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
			nswidgets.MutedText(cx, func() string { return tr("Unable to load conversation") }),
			nswidgets.MutedText(cx, func() string { return v.Thread().Status }),
			nswidgets.Quiet(tr("Retry"), "", func() {
				if v.OnReload != nil {
					v.OnReload()
				}
			}),
		}}
	case "loading":
		return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, AlignSelf: core.AlignCenter, Gap: 8}, Children: []core.Node{
			ui.Spinner{Size: 14},
			nswidgets.MutedText(cx, func() string { return tr("Loading conversation…") }),
		}}
	case "hidden":
		return nswidgets.MutedText(cx, func() string { return tr("No visible messages (reasoning-only or unsupported items)") })
	}
	return core.View{}
}

// groupRow is a user prompt or the agent's response to it.
func (v View) groupRow(cx *core.Scope, group func() msgfmt.MessageGroup) core.Node {
	user := core.NewMemo(cx, func() bool { return group().User })
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if user.Get() {
			first := func() model.Message {
				if g := group(); len(g.Messages) > 0 {
					return g.Messages[0]
				}
				return model.Message{}
			}
			if tasks := msgfmt.DelegationResults(first()); len(tasks) > 0 {
				return v.delegationReceipt(cx, tasks)
			}
			body := v.userMessage(cx, first)
			if v.UserContent != nil {
				body = v.UserContent(cx, first, body)
			}
			return core.View{Style: core.Style{Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{
				core.Show{When: func() bool { return group().ShowTimestamp }, Then: func(cx *core.Scope) core.Node {
					return core.Text{TestID: "timestamp-" + strconv.Itoa(core.Untracked(cx, func() int { return group().ID })), ContentFunc: func() string { return msgfmt.FormatMessageTimestamp(group().Timestamp) }, Style: core.Style{FontSize: 11, AlignSelf: core.AlignEnd}, StyleFunc: func() core.Style { return core.Style{Color: nswidgets.ColorsOf(cx).Muted()} }}
				}}, body,
			}}
		}
		runs := func() []msgfmt.ActivityRun { return msgfmt.ActivityRuns(group().Messages) }
		return core.View{Style: core.Style{Gap: 12, MinWidth: core.Pt(0)}, Children: []core.Node{
			core.Each(runs, func(r msgfmt.ActivityRun) int { return r.ID },
				func(cx *core.Scope, run func() msgfmt.ActivityRun, _ func() int) core.Node {
					return v.runRow(cx, run)
				}),
			// A finished response can be copied whole or handed off.
			core.Show{When: func() bool { g := group(); return !g.Streaming && len(g.Messages) > 0 }, Then: func(cx *core.Scope) core.Node {
				var extra []core.Node
				if v.OnHandoff != nil {
					extra = append(extra, chat.IconAction{Label: tr("Hand off to a new conversation"), Icon: nswidgets.IconOf("arrow-right"),
						OnPress: v.OnHandoff, TestID: "handoff"})
				}
				return chat.Actions{Text: func() string { return msgfmt.MessageGroupText(group()) }, Children: extra,
					TestID: "actions-" + strconv.Itoa(core.Untracked(cx, func() int { return group().ID }))}
			}},
		}}
	})
}

// runRow is one message, or consecutive activities that fold under a count
// and expand only when the user asks.
func (v View) runRow(cx *core.Scope, run func() msgfmt.ActivityRun) core.Node {
	batch := core.NewMemo(cx, func() bool { r := run(); return r.Activity && len(r.Messages) > 1 })
	messages := func() []model.Message { return run().Messages }
	each := func() core.Node {
		return core.Each(messages, func(m model.Message) int { return m.ID },
			func(cx *core.Scope, m func() model.Message, _ func() int) core.Node { return v.message(cx, m) })
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if !batch.Get() {
			return each()
		}
		open := core.NewSignal(cx, false)
		colors := nswidgets.ColorsOf(cx)
		return core.View{Style: core.Style{Gap: 4}, Children: []core.Node{
			core.View{Role: core.RoleButton, TestID: "run-" + strconv.Itoa(core.Untracked(cx, func() int { return run().ID })),
				Style:   core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Height: core.Pt(28)},
				OnPress: func(core.PressEvent) { open.Update(func(b bool) bool { return !b }) },
				Children: []core.Node{
					nswidgets.Icon("stack-2", 14, colors.Muted),
					nswidgets.MutedText(cx, func() string { return strconv.Itoa(len(run().Messages)) + " activities" }),
				},
			},
			// Folded activities are not built at all.
			core.Show{When: open.Get, Then: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{Gap: 4}, Children: []core.Node{each()}}
			}},
		}}
	})
}

// message draws one message by its role, built again only when the role
// changes: its text and status update in place.
func (v View) message(cx *core.Scope, m func() model.Message) core.Node {
	role := core.NewMemo(cx, func() string {
		if msgfmt.IsDelegationCall(m()) {
			return "delegation"
		}
		r := m().Role
		if r != "memory" && r != "error" && r != "user" && r != "assistant" {
			return "activity"
		}
		return r
	})
	return core.Dynamic(func(cx *core.Scope) core.Node {
		switch role.Get() {
		case "delegation":
			return v.delegationMessage(cx, m)
		case "assistant":
			return v.assistant(cx, m)
		case "user":
			return v.userMessage(cx, m)
		case "error":
			return v.errorCard(cx, m)
		case "memory":
			return core.Show{When: func() bool { mm := m().Memory; return mm != nil && mm.Explicit }, Then: func(cx *core.Scope) core.Node {
				return nswidgets.MutedText(cx, func() string {
					return tr("Miya memory changed. View it on the memory page; edit on your desktop.")
				})
			}}
		}
		return v.activity(cx, m)
	})
}

func (v View) userMessage(cx *core.Scope, m func() model.Message) core.Node {
	// Images and attached files rarely change; their row is built again
	// only when the list does.
	files := core.NewMemo(cx, func() string {
		msg := m()
		return strings.Join(msg.Images, "\x00") + "\x01" + strings.Join(msgfmt.MessageFiles(msg), "\x00")
	})
	return chat.Message{Outgoing: true, Children: []core.Node{
		v.referenceChips(cx, func() string { return msgfmt.Message(m()).Content }, nil),
		core.Dynamic(func(cx *core.Scope) core.Node {
			files.Get()
			return v.attachments(cx, core.Untracked(cx, m))
		}),
		core.Text{TestID: "prompt-" + idOf(cx, m), Selectable: core.SelectableOn,
			Style: core.Style{FontSize: 13, LineHeight: 19},
			ContentFunc: func() string {
				body, _ := model.SplitReferenceContext(msgfmt.Message(m()).Content)
				return msgfmt.NativeText(referenceText(body, v.ReferenceTitle))
			}},
	}}
}

// attachments are a prompt's images as framed thumbnails and its other
// files as chips naming them.
func (v View) attachments(cx *core.Scope, m model.Message) core.Node {
	paths := msgfmt.MessageFiles(m)
	if len(m.Images) == 0 && len(paths) == 0 {
		return core.Group(nil)
	}
	colors := nswidgets.ColorsOf(cx)
	var items []core.Node
	for i, src := range m.Images {
		source := func() string { return imageURI(src) }
		if v.ImageSource != nil {
			source = v.ImageSource(cx, src)
		}
		// The frame keeps a light image apart from the page and clips it.
		items = append(items, core.View{Style: core.Style{Width: core.Pt(80), Height: core.Pt(80), FlexShrink: 0, CornerRadius: 8, BorderWidth: 1, Overflow: core.Hidden},
			StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Line(), Background: colors.Panel()} },
			Children: []core.Node{core.Image{SourceFunc: func() core.ImageSource {
				// A source not resolved yet shows nothing rather than a broken path.
				if s := source(); s != "" {
					return core.Src(imageURI(s))
				}
				return core.ImageSource{}
			}, ResizeMode: core.Cover, Label: tr("Image preview"),
				TestID: "image-" + strconv.Itoa(m.ID) + "-" + strconv.Itoa(i),
				Style:  core.Style{Width: core.Pt(78), Height: core.Pt(78)}}}})
	}
	for _, p := range paths {
		items = append(items, chat.Attachment{Label: filepath.Base(p)})
	}
	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 8, JustifyContent: core.JustifyEnd}, Children: items}
}

// imageURI is a message image as a URI: data and file URLs as they are,
// a path as a file URL.
func imageURI(src string) string {
	for _, scheme := range []string{"data:", "file:", "http:", "https:"} {
		if strings.HasPrefix(src, scheme) {
			return src
		}
	}
	return (&url.URL{Scheme: "file", Path: src}).String()
}

// assistant is a reply's Markdown, streamed while the turn writes it: only
// the block being written renders again.
func (v View) assistant(cx *core.Scope, m func() model.Message) core.Node {
	streaming := core.NewMemo(cx, func() bool {
		t := v.Thread()
		msg := m()
		return t.Busy && msg.Status != "completed" && len(t.Messages) > 0 && t.Messages[len(t.Messages)-1].ID == msg.ID
	})
	id := "reply-" + idOf(cx, m)
	content := func() string { return model.ImagePlaceholder.ReplaceAllString(msgfmt.Message(m()).Content, "") }
	return core.Dynamic(func(cx *core.Scope) core.Node {
		return ui.TextView{TestID: id, MarkdownFunc: content, OnLinkPress: v.openLink(cx),
			Options: textview.Options{Streaming: streaming.Get()}, Selectable: true, NoCodeBlockScrollbar: true}
	})
}

func (v View) errorCard(cx *core.Scope, m func() model.Message) core.Node {
	colors := nswidgets.ColorsOf(cx)
	retryable := func() bool { return msgfmt.RetryableError(v.Thread(), m()) }
	return core.View{
		StyleFunc: func() core.Style {
			return core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Padding: core.All(16), Gap: 10, BorderWidth: 1, BorderColor: colors.Line(), CornerRadius: 16}
		},
		Children: []core.Node{
			nswidgets.Icon("alert-circle", 16, colors.WarnInk),
			core.Text{TestID: "error-" + idOf(cx, m), Selectable: core.SelectableOn, Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0)},
				ContentFunc: func() string { return msgfmt.NativeText(tr(m().Content)) }},
			core.Show{When: retryable, Then: func(cx *core.Scope) core.Node {
				return ui.IconButton{Icon: nswidgets.IconOf("refresh"), Label: tr("Retry response"), Size: 28, IconSize: 16,
					Disabled: func() bool { t := v.Thread(); return t.Busy || t.Loading },
					OnPress: func() {
						if v.OnRetry != nil {
							v.OnRetry(appmodel.RetryRequest{ThreadID: v.Thread().ID, MessageID: m().ID})
						}
					}}
			}},
		},
	}
}

// activity is a tool call, command, edit or plan as one row whose detail
// expands.
func (v View) activity(cx *core.Scope, m func() model.Message) core.Node {
	return activityRow(cx, func() chat.Activity {
		msg := m()
		p := msgfmt.Message(msg)
		a := chat.Activity{ID: strconv.Itoa(msg.ID), Action: msgfmt.NativeText(p.Title), Status: status(msg.Status)}
		// Sub-agents inside an activity show that it still works.
		if s := subagent.Summary(msg.Agents); s != "" {
			a.Detail = msgfmt.NativeText(s)
		}
		if subagent.AnyActive(msg.Agents) {
			a.Status = chat.Running
		}
		if svg, ok := nswidgets.LookupSVG(p.Icon); ok {
			a.Icon = func() *core.SVG { return svg }
		}
		hasBody := strings.TrimSpace(p.Content) != "" || len(p.Plan) > 0 || len(p.Changes) > 0
		switch {
		case len(msg.Agents) > 0:
			a.Content = func(cx *core.Scope) core.Node {
				children := []core.Node{subAgentList(cx, func() []model.SubAgent { return m().Agents })}
				// An adapter leaves the content empty when the list says it all.
				children = append(children, core.Show{When: func() bool { return strings.TrimSpace(msgfmt.Message(m()).Content) != "" },
					Then: func(cx *core.Scope) core.Node {
						return detailWithLink(cx, func() model.MessagePresentation { return msgfmt.Message(m()) }, v.openLink(cx))
					}})
				return core.View{Style: core.Style{Gap: 8, MinWidth: core.Pt(0)}, Children: children}
			}
		case hasBody:
			a.Content = func(cx *core.Scope) core.Node {
				return detailWithLink(cx, func() model.MessagePresentation { return msgfmt.Message(m()) }, v.openLink(cx))
			}
			a.Mono = len(p.Plan) == 0 && (p.Format == "" || p.Format == "text")
		}
		return a
	})
}

func status(s string) chat.Status {
	switch s {
	case "inProgress", "in_progress", "running", "pending":
		return chat.Running
	case "failed", "declined":
		return chat.Failed
	}
	return chat.Completed
}

// detail is an activity's expanded body by its format.
func detail(cx *core.Scope, p func() model.MessagePresentation) core.Node {
	return detailWithLink(cx, p, openLink(cx))
}

func detailWithLink(cx *core.Scope, p func() model.MessagePresentation, onLink func(textview.LinkPressEvent)) core.Node {
	colors := nswidgets.ColorsOf(cx)
	format := core.NewMemo(cx, func() string {
		pp := p()
		if len(pp.Plan) > 0 {
			return "plan"
		}
		return pp.Format
	})
	return core.Dynamic(func(cx *core.Scope) core.Node {
		switch format.Get() {
		case "plan":
			return core.Text{Selectable: core.SelectableOn, Style: core.Style{FontSize: 12, LineHeight: 20}, ContentFunc: func() string {
				var b strings.Builder
				for _, s := range p().Plan {
					marker := "○"
					switch s.Status {
					case "completed":
						marker = "✓"
					case "in_progress", "inProgress":
						marker = "◉"
					}
					b.WriteString(marker + " " + s.Step + "\n")
				}
				return strings.TrimSuffix(b.String(), "\n")
			}}
		case "markdown":
			return ui.TextView{MarkdownFunc: func() string { return p().Content }, Selectable: true, OnLinkPress: onLink}
		case "diff":
			return diff.DiffView{Patch: func() string { return p().Content }, Unified: true, Selectable: true}
		case "files":
			return diff.DiffView{Unified: true, Selectable: true, Patch: func() string {
				var b strings.Builder
				for _, f := range p().Changes {
					b.WriteString(f.Diff)
					if !strings.HasSuffix(f.Diff, "\n") {
						b.WriteString("\n")
					}
				}
				return b.String()
			}}
		}
		return core.Text{Selectable: core.SelectableOn, ContentFunc: func() string { return msgfmt.NativeText(p().Content) },
			StyleFunc: func() core.Style {
				return core.Style{FontFamily: nswidgets.MonoFont, FontSize: 12, LineHeight: 18, Color: colors.Ink()}
			}}
	})
}

// idOf reads a row's message ID without tracking it. Rows are keyed by ID,
// so it never changes; a tracked read would rebuild the row on every change
// of the message, as a message holds slices and notifies on each read.
func idOf(cx *core.Scope, m func() model.Message) string {
	return strconv.Itoa(core.Untracked(cx, func() int { return m().ID }))
}

// openLink opens a link a reply holds with the system.
func openLink(cx *core.Scope) func(textview.LinkPressEvent) {
	return func(e textview.LinkPressEvent) { cx.OpenURL(e.URL, nil) }
}

func (v View) openLink(cx *core.Scope) func(textview.LinkPressEvent) {
	if v.OnLink == nil {
		return openLink(cx)
	}
	return func(e textview.LinkPressEvent) { v.OnLink(e.URL) }
}
