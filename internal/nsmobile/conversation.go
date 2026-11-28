package nsmobile

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/ui/chat"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/nschat"
	"miya/internal/nswidgets"
	phoneclient "miya/internal/phone/client"
)

func (m *App) conversation(cx *core.Scope) core.Node {
	state := chat.NewConversationState(cx)
	return core.View{TestID: "mobile-conversation", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
		m.threadHeader(cx),
		nschat.View{TestID: "mobile-transcript", State: state,
			Thread: live(m, func() model.Thread { t, _ := m.PendingSendThread(m.Viewer.Current()); return t }),
			OnRetry: func(req appmodel.RetryRequest) {
				m.act(func() { r := m.ThreadRequest("retry", req.ThreadID); r.MessageID = req.MessageID; m.Action(r) })()
			},
			WaitingApproval: live(m, func() bool { return len(m.ConversationApprovals()) > 0 }),
			OnReload:        m.act(func() { m.Subscribe(m.Open) }), OnHandoff: m.act(func() {
				m.Handoff()
				if m.Open == 0 && m.Draft != "" {
					m.Path = "/thread"
				}
			}),
			ImageSource: func(_ *core.Scope, src string) func() string {
				resolve := liveImage(m)
				return func() string { return resolve(src) }
			}, OnLink: m.openMessageLink,
			Composer: m.composer(cx, state),
		},
	}}
}
func liveImage(m *App) func(string) string {
	return func(src string) string { m.revision.Get(); return m.ImagePath(src) }
}
func (m *App) composer(cx *core.Scope, state *chat.ConversationState) core.Node {
	return core.View{Style: core.Style{Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{
		core.Show{When: live(m, func() bool { return m.SendErr != "" }), Then: m.errorText},
		m.desktopPreviews(cx),
		m.pendingView(cx), m.queueView(cx), m.references(cx),
		m.requestCards(cx),
		m.thumbnails(cx),
		// A new conversation opened outside home chooses its project here;
		// home has its own picker.
		core.Show{When: live(m, func() bool { return m.Open == 0 && m.Path == "/thread" }), Then: func(cx *core.Scope) core.Node {
			return m.button("select-project", tr("Select project"), nil, func() { m.pages.Open("projects") })
		}},
		m.composerBox(cx, state),
	}}
}

// composerHasContent reports whether there is a message to send.
func (m *App) composerHasContent() bool {
	return strings.TrimSpace(m.Draft) != "" || len(m.Images) > 0
}

// composerBox is the old phone app's rounded composer: the text with + and
// the agent's icon on its left, Steer, the effort and a round send/voice button
// on its right. Once the text has a line break or wraps, or Steer shows, the
// text takes the whole width above the tools. The native editor stays mounted
// across both layouts.
func (m *App) composerBox(cx *core.Scope, state *chat.ConversationState) core.Node {
	colors := nswidgets.ColorsOf(cx)
	editor := &core.Ref{}
	m.composerInput = editor
	cx.Cleanup(func() {
		if m.composerInput == editor {
			m.composerInput, m.composing = nil, false
		}
	})
	draft := core.NewMemo(cx, live(m, func() string { return m.Draft }))
	focused := core.NewSignal(cx, false)
	agent := core.NewMemo(cx, live(m, m.Agent))
	busy := core.NewMemo(cx, live(m, func() bool { return m.Viewer.Current().Busy }))
	content := core.NewMemo(cx, live(m, m.composerHasContent))
	steer := core.NewMemo(cx, func() bool { return busy.Get() && content.Get() })
	effort := core.NewMemo(cx, live(m, func() string {
		if steer.Get() || cx.Env().Window().Width < 360 {
			return ""
		}
		return strings.ToUpper(m.SendOptions(m.Agent()).Effort)
	}))
	// wrapped holds the full-width layout from the length at which the text
	// wrapped, so the wider text does not flip it back.
	wrapped := core.NewSignal(cx, false)
	textHeight := core.NewSignal(cx, core.Points(0))
	var line core.Points
	expandAt := 0
	cx.Effect(func() {
		m.revision.Get()
		if wrapped.GetUntracked() && utf8.RuneCountInString(m.Draft) < expandAt {
			wrapped.Set(false)
		}
	})
	expanded := core.NewMemo(cx, live(m, func() bool { return steer.Get() || wrapped.Get() || strings.Contains(m.Draft, "\n") }))
	type sendLook struct {
		icon, label string
		ink         bool
	}
	look := core.NewMemo(cx, live(m, func() sendLook {
		switch {
		case m.Sending:
			return sendLook{"", tr("Send"), true}
		case busy.Get() && !content.Get():
			return sendLook{"x", tr("Stop"), true}
		case busy.Get():
			return sendLook{"arrow-up", tr("Add to queue"), true}
		case content.Get() && m.Connected:
			return sendLook{"arrow-up", tr("Send"), true}
		case !content.Get() && m.Connected:
			return sendLook{"microphone", tr("Hold to speak"), false}
		}
		return sendLook{"arrow-up", tr("Send"), false}
	}))
	// quiet keeps the press that ends a recording from also sending.
	var quiet time.Time
	hold := m.voiceHold(cx, &quiet)
	send := core.View{TestID: "send", Role: core.RoleButton, LabelFunc: func() string { return look.Get().label },
		Disabled: live(m, func() bool { return m.composing || m.Sending || !m.Connected && !m.composerHasContent() }),
		OnPress: func(core.PressEvent) {
			if time.Now().Before(quiet) {
				return
			}
			m.act(func() {
				if m.composing || m.Sending || m.voice != nil {
					return
				}
				if !m.composerHasContent() {
					if m.Viewer.Current().Busy {
						m.Stop()
					}
					return
				}
				m.Send()
				if m.Sending {
					cx.DismissKeyboard()
				}
				state.JumpToLatest()
			})()
		},
		StyleFunc: func() core.Style {
			st := core.Style{Width: core.Pt(44), Height: core.Pt(44), CornerRadius: 22, FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Background: colors.Line()}
			if look.Get().ink {
				st.Background = colors.Ink()
			}
			return st
		},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			l := look.Get()
			if l.icon == "" {
				return ui.Spinner{Size: 18, Style: core.Style{Color: colors.Canvas()}}
			}
			tint := colors.Muted
			if l.ink {
				tint = colors.Canvas
			}
			return nswidgets.Icon(l.icon, 20, tint)
		})},
	}
	tool := func(id, label string, icon core.Node, press func()) core.Node {
		return core.View{TestID: id, Role: core.RoleButton, Label: label, OnPress: func(core.PressEvent) { m.act(press)() },
			Style:    core.Style{Width: core.Pt(44), Height: core.Pt(44), CornerRadius: 22, FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{icon}}
	}
	options := func() { m.optionsAgent = m.Agent(); m.pages.Open("options") }
	input := base.Input{Ref: editor, Value: draft.Get, OnChange: func(s string) {
		m.caret = composer.ComposerEditEnd(m.Draft, s)
		m.anchor = m.caret
		m.Draft = s
		m.change()
	}, Label: tr("Message"), PlaceholderFunc: live(m, func() string {
		if name := m.SendOptions(m.Agent()).Model; name != "" {
			return name
		}
		return m.agentName(m.Agent())
	}), TestID: "mobile-composer", Multiline: true, SubmitBehavior: core.SubmitBehaviorNewline,
		OnFocus: func() { focused.Set(true) },
		OnBlur:  func() { focused.Set(false) },
		// The keyboard owns selection while editing, including marked text.
		// Feeding its reported range back can interrupt native composition.
		OnSelectionChange: func(r core.TextRange) {
			anchor, caret := utf16ByteOffset(m.Draft, r.Start), utf16ByteOffset(m.Draft, r.End)
			if m.anchor != anchor || m.caret != caret {
				m.anchor, m.caret = anchor, caret
				m.change()
			}
		},
		OnCompositionChange: func(c core.Composition) {
			if c.Composing != m.composing {
				m.composing = c.Composing
				m.change()
			}
		},
		OnContentSizeChange: func(size core.Size) {
			textHeight.Set(size.Height)
			if m.Draft == "" {
				line = size.Height
				return
			}
			if line <= 0 {
				line = 21
			}
			if !wrapped.GetUntracked() && size.Height > line*1.5 {
				expandAt = utf8.RuneCountInString(m.Draft)
				wrapped.Set(true)
			}
		},
		StyleFunc: func() core.Style {
			st := core.Style{Width: core.Pct(100), Height: core.Pt(56), FontSize: 16, Color: colors.Ink(), TextAlignVertical: core.TextAlignVerticalTop}
			if expanded.Get() {
				st.Padding = core.Edges{Top: 14, Left: 14, Right: 14, Bottom: 6}
				// An explicit height bounds both the native scroll viewport and
				// the card's intrinsic height during parent layout.
				st.Height = core.Pt(min(120, max(40, textHeight.Get()+st.Padding.Top+st.Padding.Bottom)))
			} else {
				right := core.Points(6 + 44 + 8)
				if e := effort.Get(); e != "" {
					right += core.Points(len(e))*8 + 12
				}
				st.Padding = core.Edges{Top: 17, Bottom: 17, Left: 96, Right: right}
			}
			return st
		}}
	return core.View{TestID: "mobile-composer-box", StyleFunc: func() core.Style {
		return core.Style{Width: core.Pct(100), MinHeight: core.Pt(56), CornerRadius: 26, Background: colors.Bubble()}
	}, Children: []core.Node{
		input,
		// An unfocused, empty editor accepts a tap or a voice hold. Keep
		// the native field underneath so it cannot take this touch and open the IME.
		core.Show{When: live(m, func() bool {
			return m.voicePage() && !focused.Get() && (draft.Get() == "" || m.voice != nil)
		}), Then: func(*core.Scope) core.Node {
			return core.View{NoFocus: true,
				Style:   core.Style{Position: core.Absolute, Top: core.Pt(0), Bottom: core.Pt(0), Left: core.Pt(0), Right: core.Pt(0)},
				OnTouch: func(e core.TouchEvent) { hold(e, m.Draft == "") },
				OnPress: func(core.PressEvent) {
					if m.voice == nil && !time.Now().Before(quiet) {
						editor.Focus()
					}
				},
			}
		}},
		core.View{PointerEvents: core.PointerEventsBoxNone, StyleFunc: func() core.Style {
			st := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifySpaceBetween,
				Padding: core.Edges{Left: 4, Right: 6, Bottom: 6}}
			if !expanded.Get() {
				st.Position, st.Left, st.Right, st.Bottom = core.Absolute, core.Pt(0), core.Pt(0), core.Pt(0)
			}
			return st
		}, Children: []core.Node{
			core.View{PointerEvents: core.PointerEventsBoxNone, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter}, Children: []core.Node{
				tool("add-image", tr("Add image"), nswidgets.Icon("plus", 20, colors.Muted), m.addPhotos),
				tool("model-options", tr("Model settings"), core.Dynamic(func(cx *core.Scope) core.Node { return nswidgets.AgentIcon(agent.Get(), 20) }), options),
			}},
			core.View{PointerEvents: core.PointerEventsBoxNone, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4}, Children: []core.Node{
				core.Show{When: steer.Get, Then: func(cx *core.Scope) core.Node {
					return core.View{TestID: "steer", Role: core.RoleButton, Label: tr("Steer now"),
						Disabled: live(m, func() bool { return m.composing || m.voice != nil || !m.Connected || m.Sending }),
						OnPress: func(core.PressEvent) {
							m.act(func() {
								if !m.composing && m.voice == nil && m.Connected && !m.Sending && m.composerHasContent() {
									m.Steer()
									if m.Sending {
										cx.DismissKeyboard()
									}
								}
							})()
						},
						StyleFunc: func() core.Style {
							return core.Style{Width: core.Pt(64), Height: core.Pt(44), CornerRadius: 22, FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Background: colors.Hover()}
						},
						Children: []core.Node{core.Text{Content: tr("Steer"), MaxLines: 1, StyleFunc: func() core.Style { return core.Style{FontSize: 12, FontWeight: 600, Color: colors.Ink()} }}}}
				}},
				core.Show{When: func() bool { return effort.Get() != "" }, Then: func(cx *core.Scope) core.Node {
					return core.View{TestID: "composer-effort", Role: core.RoleButton, Label: tr("Model settings"), OnPress: func(core.PressEvent) { m.act(options)() },
						Style:    core.Style{MinHeight: core.Pt(44), Padding: core.Edges{Left: 4, Right: 4}, JustifyContent: core.JustifyCenter, FlexShrink: 0},
						Children: []core.Node{core.Text{ContentFunc: effort.Get, MaxLines: 1, StyleFunc: func() core.Style { return core.Style{FontSize: 12, FontWeight: 500, Color: colors.Muted()} }}}}
				}},
				core.View{TestID: "voice-hold", Style: core.Style{FlexShrink: 0},
					OnTouch:  func(e core.TouchEvent) { hold(e, !m.Viewer.Current().Busy) },
					Children: []core.Node{send}},
			}},
		}},
	}}
}

// thumbnails are the photos to send, above the composer, each with its
// remove badge in the corner.
func (m *App) thumbnails(cx *core.Scope) core.Node {
	return core.Show{When: live(m, func() bool { return len(m.Images) > 0 }), Then: func(cx *core.Scope) core.Node {
		colors := nswidgets.ColorsOf(cx)
		return core.Scroll{Horizontal: true, Style: core.Style{Height: core.Pt(64), FlexShrink: 0}, Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, Gap: 8, Padding: core.Edges{Left: 4, Right: 4}}, Children: []core.Node{
				core.Each(live(m, func() []string { return m.Images }), func(p string) string { return p }, func(cx *core.Scope, p func() string, index func() int) core.Node {
					return core.View{StyleFunc: func() core.Style {
						return core.Style{Width: core.Pt(64), Height: core.Pt(64), CornerRadius: 12, FlexShrink: 0, Background: colors.Bubble()}
					}, Children: []core.Node{
						core.Image{SourceFunc: func() core.ImageSource { return core.Src(imageURI(p())) }, Label: tr("Image preview"), ResizeMode: core.Cover, Style: core.Style{Width: core.Pt(64), Height: core.Pt(64), CornerRadius: 12}},
						core.View{TestID: "remove-image-" + strconv.Itoa(index()), Role: core.RoleButton, Label: tr("Remove image"),
							OnPress: func(core.PressEvent) { m.act(func() { m.RemoveImage(index()) })() },
							Style:   core.Style{Position: core.Absolute, Top: core.Pt(0), Right: core.Pt(0), Width: core.Pt(44), Height: core.Pt(44), AlignItems: core.AlignEnd, Padding: core.All(4)},
							Children: []core.Node{core.View{Style: core.Style{Width: core.Pt(22), Height: core.Pt(22), CornerRadius: 11, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Background: core.RGBA(0x000000, 0.6)},
								Children: []core.Node{nswidgets.Icon("x", 12, func() core.Color { return core.RGB(0xffffff) })}}}},
					}}
				}),
			}},
		}}
	}}
}
func (m *App) pendingView(cx *core.Scope) core.Node {
	return core.Each(live(m, m.Pending), func(p phoneclient.PendingStatus) int { return p.ID }, func(cx *core.Scope, p func() phoneclient.PendingStatus, _ func() int) core.Node {
		return column(core.Text{ContentFunc: func() string {
			v := p()
			if v.Failure != "" {
				return v.Failure
			}
			if v.Acknowledged {
				return tr("Syncing conversation…")
			}
			return v.Stage
		}, Style: core.Style{FontSize: 14}},
			core.Show{When: func() bool { return p().Failure != "" }, Then: func(cx *core.Scope) core.Node {
				return row(
					m.button("retry-send-"+strconv.Itoa(p().ID), tr("Retry send"), live(m, func() bool { return m.Sending }), func() { m.RetryPending(p().ID) }),
					m.button("edit-send-"+strconv.Itoa(p().ID), tr("Edit message"), live(m, func() bool { return m.Sending || m.Draft != "" || len(m.Images) > 0 }), func() { m.EditPending(p().ID) }),
				)
			}},
		)
	})
}
func (m *App) queueView(cx *core.Scope) core.Node {
	return core.Show{When: live(m, func() bool { return len(m.Viewer.Current().Queue) > 0 }), Then: func(cx *core.Scope) core.Node {
		return core.Scroll{Style: core.Style{MaxHeight: core.Pt(160)}, Children: []core.Node{column(
			core.Text{TestID: "queue-count", ContentFunc: live(m, func() string { return fmt.Sprintf(tr("%d messages queued"), len(m.Viewer.Current().Queue)) })},
			core.Show{When: live(m, func() bool { t := m.Viewer.Current(); return !t.Busy && t.StopRequested }), Then: func(cx *core.Scope) core.Node {
				return m.button("drain-queue", tr("Continue sending"), nil, func() { m.QueueAction("drain-queue", 0) })
			}},
			core.Each(live(m, func() []model.QueuedMessage { return m.Viewer.Current().Queue }), func(q model.QueuedMessage) int { return q.ID }, func(cx *core.Scope, q func() model.QueuedMessage, _ func() int) core.Node {
				id := core.Untracked(cx, func() int { return q().ID })
				return column(core.Text{TestID: "queued-" + strconv.Itoa(id), ContentFunc: func() string { return appmodel.ThreadContextDisplay(q().Text) }, MaxLines: 2},
					row(core.Show{When: live(m, func() bool { return m.Viewer.Current().Busy }), Then: func(cx *core.Scope) core.Node {
						return m.button("steer-queued-"+strconv.Itoa(id), tr("Steer queued message now"), nil, func() { m.QueueAction("steer-queued", id) })
					}},
						m.button("dequeue-"+strconv.Itoa(id), tr("Delete queued message"), nil, func() { m.QueueAction("dequeue", id) })))
			}),
		)}}
	}}
}
