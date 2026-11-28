// Package nschat is the conversation view on NSGo, shared by the desktop and
// the phone. Grouping and presentation stay
// in internal/chat/presentation; this package only draws them.
package nschat

import (
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/ui/chat"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/nswidgets"
	"miya/internal/textutil"
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
	// NewConversation replaces the transcript on a desktop draft without a thread.
	NewConversation core.Node
	// OnLink handles thread and desktop-file references in the owning surface.
	OnLink func(string)
	// Requests are the pending approvals and questions, above the composer.
	Requests core.Node
	// MemoryNotice allows desktop callers to show details and actions for explicit changes.
	MemoryNotice func(*core.Scope, model.MemoryNotice) core.Node
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
	// ComposerOwner resets editor undo and desktop transcript rows when the workspace or conversation changes.
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
	activityOpen    map[activityDisclosure]bool
	preview         *core.Signal[imagePreview]
}

// maxWidth is the column the conversation keeps to on wide windows.
const maxWidth = 744

// Render implements core.Node.
func (v View) Render(cx *core.Scope) core.Node {
	if v.State == nil {
		v.State = chat.NewConversationState(cx)
	}
	v.preview = core.NewSignal(cx, imagePreview{})
	previewOwner := core.NewMemo(cx, func() string {
		if v.ComposerOwner != nil {
			return v.ComposerOwner()
		}
		return strconv.Itoa(v.Thread().ID)
	})
	cx.Effect(func() {
		previewOwner.Get()
		v.preview.Set(imagePreview{})
	})
	imageDialog := v.imageDialog(cx)

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
	groups := core.NewMemoFunc(cx, func() []msgfmt.MessageGroup {
		t := v.Thread()
		return msgfmt.ThreadMessageGroups(t, msgfmt.GroupMessages(t.Messages))
	}, nil).Get
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
	latest := core.NewMemoFunc(cx, func() msgfmt.MessageGroup {
		all := groups()
		if last := len(all) - 1; !v.Thread().Busy && last >= 0 && !all[last].User {
			return all[last]
		}
		return msgfmt.MessageGroup{}
	}, sameGroup).Get
	status := core.NewMemo(cx, func() string { return turnStatus(v.Thread(), latest(), now.Get()) })
	height := core.Points(28)
	if !cx.Env().Desktop() {
		height = 44
	}
	floating := []core.Node{
		core.View{TestID: "turn-status", StyleFunc: func() core.Style {
			return core.Style{Display: displayIf(status.Get() != "" || len(latest().Messages) > 0),
				Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, MinHeight: core.Pt(height), MinWidth: core.Pt(0)}
		}, Children: []core.Node{
			core.Show{When: func() bool { return status.Get() != "" }, Then: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0)},
					Children: []core.Node{nswidgets.WorkingLabel(cx, "clock", status.Get)}}
			}},
			diffCounts(cx, func() model.DiffLineStats { return latest().DiffStats }, true),
			core.Show{When: func() bool { return len(latest().Messages) > 0 }, Then: func(cx *core.Scope) core.Node {
				return v.responseActions(cx, latest)
			}},
		}},
		core.Show{When: func() bool { t := v.Thread(); return !t.Busy && len(t.BackgroundTasks) > 0 }, Then: func(cx *core.Scope) core.Node {
			return core.View{Style: core.Style{Gap: 4}, Children: []core.Node{
				nswidgets.WorkingLabel(cx, "clock", func() string {
					return fmt.Sprintf(tr("Background tasks running (%d) · Waiting for results"), len(v.Thread().BackgroundTasks))
				}),
				core.Text{MaxLines: 2, ContentFunc: func() string { return backgroundTasks(v.Thread()) },
					StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: nswidgets.ColorsOf(cx).Muted()} }},
			}}
		}},
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
	transcript := func(cx *core.Scope) core.Node {
		local := v
		local.activityOpen = make(map[activityDisclosure]bool)
		return column(
			local.transcriptRows(cx, groups, latest),
			core.Dynamic(func(cx *core.Scope) core.Node { return local.emptyState(cx, empty.Get()) }),
		)
	}
	// On a desktop the composer docks under the transcript, as Miya's window
	// always had it: the status lines read against the window and no message
	// shows through between them. A phone floats it on glass.
	if cx.Env().Desktop() {
		showIntro := core.NewMemo(cx, func() bool { return v.NewConversation != nil && v.Thread().ID == 0 })
		bodyTranscript := core.Dynamic(func(cx *core.Scope) core.Node {
			if v.ComposerOwner != nil {
				v.ComposerOwner()
			}
			return chat.Conversation{State: v.State, JumpLabel: tr("↓ Latest"), TestID: "chat-transcript", Children: []core.Node{transcript(cx)}}
		})
		var body core.Node = bodyTranscript
		if v.NewConversation != nil {
			body = core.Show{When: showIntro.Get, Then: func(*core.Scope) core.Node { return v.NewConversation }, Else: func(*core.Scope) core.Node { return bodyTranscript }}
		}
		dockStyle := func() core.Style {
			padding := core.Edges{Right: 20, Bottom: 12, Left: 20}
			if showIntro.Get() {
				padding = core.Edges{Right: 28, Bottom: 10, Left: 28}
			}
			return core.Style{FlexShrink: 0, AlignItems: core.AlignCenter, Padding: padding}
		}
		return core.View{TestID: v.TestID, Style: ui.FillRest, Children: []core.Node{
			body,
			core.View{StyleFunc: dockStyle, Children: []core.Node{
				core.View{TestID: "composer-dock", Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(maxWidth), MinWidth: core.Pt(0)}, Children: []core.Node{nswidgets.Stack(cx, 8, floating...)}},
			}},
			imageDialog,
		}}
	}
	return core.Group{chat.FloatingConversation{State: v.State, JumpLabel: tr("↓ Latest"), TestID: v.TestID,
		Children: []core.Node{transcript(cx)},
		Composer: []core.Node{nswidgets.Stack(cx, 6, floating...)},
	}, imageDialog}
}

// Retain small transcripts for text selection. Long histories use the same
// scroll viewport and render only the nearby turns, except during Find when
// native text search needs to see every reply.
func (v View) transcriptRows(cx *core.Scope, groups func() []msgfmt.MessageGroup, latest func() msgfmt.MessageGroup) core.Node {
	row := func(cx *core.Scope, read func() msgfmt.MessageGroup) core.Node {
		group := core.NewMemoFunc(cx, read, sameGroup).Get
		id := core.Untracked(cx, func() int { return group().ID })
		return v.groupRow(cx, group, func() bool {
			g := latest()
			return len(g.Messages) == 0 || g.ID != id
		})
	}
	return core.Show{
		When: func() bool {
			search := v.State.Search()
			return len(groups()) > 32 && !(search.IsOpen() && search.Query() != "")
		},
		Then: func(*core.Scope) core.Node {
			return core.List{
				TestID: "transcript-turns", Viewport: core.ListViewportParent, Gap: 28, EstimatedSize: 150,
				Count: func() int { return len(groups()) }, Key: func(i int) any { return groups()[i].ID },
				Row: func(cx *core.Scope, item core.ListRow) core.Node {
					read := func() msgfmt.MessageGroup {
						all, i := groups(), item.Index()
						if i < 0 || i >= len(all) {
							return msgfmt.MessageGroup{}
						}
						return all[i]
					}
					type rowKey struct {
						id, thread int
						valid      bool
					}
					key := core.NewMemo(cx, func() rowKey {
						g := read()
						return rowKey{id: g.ID, thread: v.Thread().ID, valid: len(g.Messages) > 0}
					})
					return core.Dynamic(func(cx *core.Scope) core.Node {
						if !key.Get().valid {
							return nil
						}
						return row(cx, read)
					})
				},
			}
		},
		Else: func(*core.Scope) core.Node {
			return core.Each(groups, func(g msgfmt.MessageGroup) int { return g.ID },
				func(cx *core.Scope, group func() msgfmt.MessageGroup, _ func() int) core.Node { return row(cx, group) })
		},
	}
}

func sameGroup(a, b msgfmt.MessageGroup) bool { return reflect.DeepEqual(a, b) }

func diffCounts(cx *core.Scope, stats func() model.DiffLineStats, showZero bool) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.Show{When: func() bool { return stats() != (model.DiffLineStats{}) }, Then: func(cx *core.Scope) core.Node {
		return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexShrink: 0}, Children: []core.Node{
			core.Show{When: func() bool { return showZero || stats().Added > 0 }, Then: func(*core.Scope) core.Node {
				return core.Text{ContentFunc: func() string { return "+" + strconv.Itoa(stats().Added) },
					StyleFunc: func() core.Style { return core.Style{FontSize: 11, Color: colors.Success()} }}
			}},
			core.Show{When: func() bool { return showZero || stats().Deleted > 0 }, Then: func(*core.Scope) core.Node {
				return core.Text{ContentFunc: func() string { return "−" + strconv.Itoa(stats().Deleted) },
					StyleFunc: func() core.Style { return core.Style{FontSize: 11, Color: colors.Danger()} }}
			}},
		}}
	}}
}

func displayIf(on bool) core.Display {
	if on {
		return core.DisplayFlex
	}
	return core.DisplayNone
}

// turnStatus is the label above the composer: the running turn's progress,
// or how long the latest completed response took.
func turnStatus(t model.Thread, latest msgfmt.MessageGroup, now time.Time) string {
	if t.Busy {
		if label := msgfmt.ThreadReconnectLabel(t); label != "" {
			return label
		}
		return msgfmt.ThreadWorkingLabelAt(t, now)
	}
	if len(t.BackgroundTasks) > 0 {
		return ""
	}
	return msgfmt.TurnElapsedLabel(latest.Messages, false)
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
		names = append(names, textutil.ClipRunes(strings.Join(strings.Fields(task.Description), " "), 100))
	}
	return strings.Join(names, " · ")
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
func (v View) groupRow(cx *core.Scope, group func() msgfmt.MessageGroup, inlineFooter func() bool) core.Node {
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
			core.Show{When: func() bool { return group().ShowTimestamp }, Then: func(cx *core.Scope) core.Node {
				return core.Text{ContentFunc: func() string { return msgfmt.FormatMessageTimestamp(group().Timestamp) },
					StyleFunc: func() core.Style { return core.Style{FontSize: 11, Color: nswidgets.ColorsOf(cx).Muted()} }}
			}},
			core.Each(runs, func(r msgfmt.ActivityRun) int { return r.ID },
				func(cx *core.Scope, run func() msgfmt.ActivityRun, _ func() int) core.Node {
					return v.runRow(cx, run)
				}),
			core.Show{When: func() bool { return inlineFooter() && !group().Streaming && len(group().Messages) > 0 }, Then: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Wrap: core.Wrap, Gap: 8}, Children: []core.Node{
					core.Text{ContentFunc: func() string { return msgfmt.TurnElapsedLabel(group().Messages, false) },
						StyleFunc: func() core.Style { return core.Style{FontSize: 11, Color: nswidgets.ColorsOf(cx).Muted()} }},
					diffCounts(cx, func() model.DiffLineStats { return group().DiffStats }, true),
					v.responseActions(cx, group),
				}}
			}},
		}}
	})
}

func (v View) responseActions(cx *core.Scope, group func() msgfmt.MessageGroup) core.Node {
	var extra []core.Node
	if v.OnHandoff != nil {
		extra = append(extra, chat.IconAction{Label: tr("Hand off to a new conversation"), Icon: nswidgets.IconOf("arrow-right"),
			OnPress: v.OnHandoff, TestID: "handoff"})
	}
	id := core.NewMemo(cx, func() int { return group().ID })
	return core.Dynamic(func(cx *core.Scope) core.Node {
		return chat.Actions{Text: func() string { return msgfmt.MessageGroupText(group()) }, Children: extra,
			TestID: "actions-" + strconv.Itoa(id.Get())}
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
				if v.MemoryNotice != nil {
					return core.Dynamic(func(cx *core.Scope) core.Node {
						if mm := m().Memory; mm != nil && mm.Explicit {
							return v.MemoryNotice(cx, *mm)
						}
						return nil
					})
				}
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
	prompt := core.NewMemo(cx, func() string {
		text, _ := model.SplitReferenceContext(msgfmt.Message(m()).Content)
		return text
	})
	body := func() string { return msgfmt.NativeText(referenceText(prompt.Get(), v.ReferenceTitle)) }
	return chat.Message{Outgoing: true, Children: []core.Node{
		v.referenceChips(cx, prompt.Get, nil),
		core.Dynamic(func(cx *core.Scope) core.Node {
			files.Get()
			return v.attachments(cx, core.Untracked(cx, m))
		}),
		core.Show{When: func() bool { return body() != "" }, Then: func(cx *core.Scope) core.Node {
			style := core.Style{FontSize: 13, LineHeight: 19}
			if !cx.Env().Desktop() {
				style.FontSize, style.LineHeight = phoneMessageSize, phoneMessageLineHeight
			}
			return chat.Bubble{Children: []core.Node{
				core.Text{TestID: "prompt-" + idOf(cx, m), Selectable: core.SelectableOn, Style: style, ContentFunc: body},
			}}
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
		source := v.resolveImage(cx, src)
		// The frame keeps a light image apart from the page and clips it.
		id := strconv.Itoa(m.ID) + "-" + strconv.Itoa(i)
		label := tr("Image preview")
		items = append(items, core.View{TestID: "preview-image-" + id, Label: label, Role: core.RoleButton,
			Disabled: func() bool { return source() == "" },
			OnPress: func(core.PressEvent) {
				if uri := source(); uri != "" {
					v.preview.Set(imagePreview{uri: uri, resolved: true})
				}
			},
			Style:     core.Style{Width: core.Pt(80), Height: core.Pt(80), FlexShrink: 0, CornerRadius: 8, BorderWidth: 1, Overflow: core.Hidden, Cursor: core.CursorPointer},
			StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Line(), Background: colors.Panel()} },
			Children: []core.Node{core.Image{SourceFunc: func() core.ImageSource {
				// A source not resolved yet shows nothing rather than a broken path.
				if s := source(); s != "" {
					return core.Src(s)
				}
				return core.ImageSource{}
			}, ResizeMode: core.Cover, ElementsHidden: true,
				TestID: "image-" + id,
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
			OnImagePress: v.previewMarkdownImage,
			Styles:       transcriptMarkdownStyles(cx),
			Options:      textview.Options{Streaming: streaming.Get()}, Selectable: true, NoCodeBlockScrollbar: true}
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
