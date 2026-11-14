package nsmobile

import (
	"fmt"
	"strconv"
	"strings"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/nschat"
	"miya/internal/nswidgets"
	phoneclient "miya/internal/phone/client"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui/chat"
)

func (m *App) conversation(cx *core.Scope) core.Node {
	state := chat.NewConversationState(cx)
	return core.View{TestID: "mobile-conversation", Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
		m.toolbar(live(m, func() string {
			if m.Title != "" {
				return m.Title
			}
			return tr("New conversation")
		}), true),
		nswidgets.MutedText(cx, live(m, m.ConnectionLine)),
		nschat.View{TestID: "mobile-transcript", State: state,
			Thread: live(m, func() model.Thread { t, _ := m.PendingSendThread(m.Viewer.Current()); return t }),
			OnRetry: func(req appmodel.RetryRequest) {
				m.act(func() { r := m.ThreadRequest("retry", req.ThreadID); r.MessageID = req.MessageID; m.Action(r) })()
			},
			OnReload: m.act(func() { m.Subscribe(m.Open) }), OnHandoff: m.act(func() {
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
	return column(
		m.errorText(cx),
		m.pendingView(cx), m.queueView(cx), m.references(cx),
		// TODO(approvals): show pending request cards here; the desktop remains
		// their owner. No automatic response is sent by this surface.
		core.Show{When: live(m, func() bool { return len(m.State.Approvals) > 0 }), Then: func(cx *core.Scope) core.Node { return text(tr("Review pending approvals on your desktop")) }},
		core.Show{When: live(m, func() bool { return len(m.Images) > 0 }), Then: func(cx *core.Scope) core.Node {
			return core.Scroll{Horizontal: true, Style: core.Style{Height: core.Pt(72)}, Children: []core.Node{row(core.Each(live(m, func() []string { return m.Images }), func(p string) string { return p }, func(cx *core.Scope, p func() string, index func() int) core.Node {
				return row(core.Image{SourceFunc: func() core.ImageSource { return core.Src(imageURI(p())) }, Label: tr("Image preview"), Style: core.Style{Width: core.Pt(64), Height: core.Pt(64), CornerRadius: 8}},
					m.button("remove-image-"+strconv.Itoa(index()), tr("Remove image"), nil, func() { m.RemoveImage(index()) }))
			}))}}
		}},
		base.Input{Value: live(m, func() string { return m.Draft }), OnChange: func(s string) { m.caret = composer.ComposerEditEnd(m.Draft, s); m.Draft = s; m.change() }, Label: tr("Message"), Placeholder: tr("Ask for follow-up changes"), TestID: "mobile-composer", Multiline: true, SubmitBehavior: core.SubmitBehaviorNewline,
			OnSelectionChange: func(r core.TextRange) { m.caret = utf16ByteOffset(m.Draft, r.End); m.change() },
			Style:             core.Style{MinHeight: core.Pt(48), MaxHeight: core.Pt(180), FontSize: 17, Padding: core.All(12), TextAlignVertical: core.TextAlignVerticalTop}},
		core.Show{When: live(m, func() bool { return m.Open == 0 }), Then: func(cx *core.Scope) core.Node {
			return m.button("select-project", tr("Select project"), nil, func() { m.sheet.Set("projects") })
		}},
		row(
			// TODO(voice): add the cancellable recording accessory here.
			m.button("add-image", tr("Add image"), nil, m.addPhotos),
			core.Text{ContentFunc: live(m, func() string { return m.agentName(m.Agent()) + " · " + m.SendOptions(m.Agent()).Model }), Style: core.Style{FontSize: 13, FlexShrink: 1}},
			m.button("model-options", tr("Model settings"), nil, func() { m.optionsAgent = m.Agent(); m.sheet.Set("options") }),
		),
		row(
			m.button("send", tr("Send"), live(m, func() bool {
				return !m.Connected || m.Sending || strings.TrimSpace(m.Draft) == "" && len(m.Images) == 0
			}), func() { m.Send(); state.JumpToLatest() }),
			core.Show{When: live(m, func() bool { return m.Viewer.Current().Busy }), Then: func(cx *core.Scope) core.Node {
				return row(
					m.button("steer", tr("Steer now"), live(m, func() bool {
						return !m.Connected || m.Sending || strings.TrimSpace(m.Draft) == "" && len(m.Images) == 0
					}), m.Steer),
					m.button("stop", tr("Stop"), live(m, func() bool { return !m.Connected }), m.Stop),
				)
			}},
		),
	)
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
