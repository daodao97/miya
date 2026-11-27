package nsmobile

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/microphone"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/nswidgets"
	"miya/internal/prefs"
	"miya/internal/voiceinput"
)

type voiceSession interface {
	Stop()
	Cancel()
}
type voiceStart func(context.Context, string, func(voiceinput.Result)) (voiceSession, error)
type phoneVoice struct {
	cancel             context.CancelFunc
	session            voiceSession
	owner              composerOwner
	draft, preview     string
	partial            string
	anchor, caret      int
	released, autoSend bool
	final              *voiceinput.Result
}
type composerOwner struct {
	link, project, agent, path string
	generation                 int
	epoch                      uint64
}

func (m *App) composerOwner() composerOwner {
	return composerOwner{m.Link, m.ComposerProject(), m.Agent(), m.Path, m.Generation, m.ComposerEpoch()}
}
func (m *App) voiceValid(v *phoneVoice) bool {
	return m.voice == v && v.owner == m.composerOwner() && m.Connected && m.Path == "/thread" && m.pages.Top() == ""
}
func (m *App) beginVoice() {
	if m.voice != nil || m.Sending || !m.Connected || m.Path != "/thread" || m.pages.Top() != "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a, b := m.anchor, m.caret
	if b < 0 || b > len(m.Draft) {
		b = len(m.Draft)
		a = b
	}
	a = max(0, min(a, len(m.Draft)))
	if a > b {
		a, b = b, a
	}
	v := &phoneVoice{cancel: cancel, owner: m.composerOwner(), draft: m.Draft, preview: m.Draft, anchor: a, caret: b, autoSend: m.voiceAutoSend}
	m.voice, m.SendErr = v, ""
	start, dir := m.voiceStart, m.DataDir
	if start == nil {
		start = func(ctx context.Context, dir string, emit func(voiceinput.Result)) (voiceSession, error) {
			return voiceinput.Start(ctx, dir, emit)
		}
	}
	go func() {
		session, err := start(ctx, dir, func(r voiceinput.Result) { m.cx.Post(func() { m.voiceResult(v, r); m.change() }) })
		m.cx.Post(func() {
			if !m.voiceValid(v) || ctx.Err() != nil {
				if session != nil {
					session.Cancel()
				}
				return
			}
			if err != nil {
				m.finishVoice(v, voiceinput.Result{Err: err, Final: true})
			} else if session == nil {
				m.cancelVoice()
			} else {
				v.session = session
				m.cx.PlayHaptic(core.HapticLight)
			}
			m.change()
		})
	}()
}
func (m *App) voiceResult(v *phoneVoice, r voiceinput.Result) {
	if !m.voiceValid(v) {
		if m.voice == v {
			m.cancelVoice()
		}
		return
	}
	if m.Draft != v.preview {
		m.cancelVoice()
		m.SendErr = tr("The draft changed during recording. Please try again.")
		return
	}
	if r.Final && (v.released || r.Err != nil) {
		m.finishVoice(v, r)
		return
	}
	m.previewVoice(v, r.Text)
	if r.Final {
		v.final = &r
	}
}
func (m *App) previewVoice(v *phoneVoice, s string) {
	v.partial = s
	v.preview = v.draft[:v.anchor] + s + v.draft[v.caret:]
	m.Draft = v.preview
	m.anchor, m.caret = v.anchor+len(s), v.anchor+len(s)
}
func (m *App) endVoice() {
	v := m.voice
	if v == nil || v.released {
		return
	}
	if !m.voiceValid(v) {
		m.cancelVoice()
		return
	}
	v.released = true
	if v.final != nil {
		m.finishVoice(v, *v.final)
	} else if v.session == nil {
		// A release during the permission prompt must never start capture later.
		m.cancelVoice()
	} else {
		v.session.Stop()
		m.cx.PlayHaptic(core.HapticLight)
	}
}
func (m *App) cancelVoice() {
	if v := m.voice; v != nil {
		m.voice = nil
		v.cancel()
		if v.session != nil {
			v.session.Cancel()
		}
		if v.owner.link == m.Link && v.owner.epoch == m.ComposerEpoch() && m.Draft == v.preview {
			m.Draft = v.draft
			m.anchor, m.caret = v.anchor, v.caret
		}
	}
}
func (m *App) finishVoice(v *phoneVoice, r voiceinput.Result) {
	if !m.voiceValid(v) {
		m.cancelVoice()
		return
	}
	if m.Draft != v.preview {
		m.cancelVoice()
		m.SendErr = tr("The draft changed during recording. Please try again.")
		return
	}
	m.cancelVoice()
	if r.Err != nil {
		m.SendErr = tr("Voice recognition failed. Check your network and microphone permission, then try again.")
		if errors.Is(r.Err, microphone.ErrDenied) {
			m.SendErr = tr("Allow microphone access in system settings to use voice input.")
		}
		return
	}
	s := strings.TrimSpace(r.Text)
	if s == "" {
		m.SendErr = tr("No speech recognized. Press and hold to try again.")
		return
	}
	m.previewVoice(v, s)
	m.cx.PlayHaptic(core.HapticSuccess)
	if v.autoSend && v.released {
		m.Send()
	}
}

// voiceHold makes child, the send button, also the push-to-talk control, as
// the old app's long press on the composer was: a finger held there 350ms
// while no reply runs records, lifting it finishes and sliding 60 points
// away cancels. quiet keeps the press that ends a recording from sending.
func (m *App) voiceHold(cx *core.Scope, quiet *time.Time, child core.Node) core.Node {
	var stop func() bool
	var origin core.Point
	stopTimer := func() {
		if stop != nil {
			stop()
			stop = nil
		}
	}
	finish := func(end func()) {
		stopTimer()
		if m.voice != nil {
			*quiet = time.Now().Add(400 * time.Millisecond)
			end()
			m.change()
		}
	}
	cx.Cleanup(func() { stopTimer(); m.cancelVoice() })
	return core.View{TestID: "voice-hold", Style: core.Style{FlexShrink: 0},
		OnTouch: func(e core.TouchEvent) {
			switch e.Phase {
			case core.TouchStart:
				if len(e.Touches) != 1 {
					finish(m.cancelVoice)
					return
				}
				origin = e.Touches[0].PagePosition
				stopTimer()
				if m.Connected && !m.Sending && !m.Viewer.Current().Busy {
					stop = cx.AfterFunc(350*time.Millisecond, func() {
						stop = nil
						m.beginVoice()
						m.change()
					})
				}
			case core.TouchMove:
				for _, t := range e.Touches {
					if dx, dy := t.PagePosition.X-origin.X, t.PagePosition.Y-origin.Y; dx*dx+dy*dy > 60*60 {
						finish(m.cancelVoice)
					}
				}
			case core.TouchEnd:
				finish(m.endVoice)
			case core.TouchCancel:
				finish(m.cancelVoice)
			}
		},
		Children: []core.Node{child}}
}

// voiceStatus keeps the recognized words visible without taking the active touch.
func (m *App) voiceStatus(cx *core.Scope) core.Node {
	return core.Show{When: live(m, func() bool { return m.voice != nil }), Then: func(cx *core.Scope) core.Node {
		colors := nswidgets.ColorsOf(cx)
		released := live(m, func() bool { return m.voice != nil && m.voice.released })
		return core.View{TestID: "voice-overlay", PointerEvents: core.PointerEventsBoxNone,
			Style: core.Style{Position: core.Absolute, Top: core.Pt(0), Bottom: core.Pt(0), Left: core.Pt(0), Right: core.Pt(0),
				AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(24), Background: ui.Black(0.4)},
			Children: []core.Node{core.View{PointerEvents: core.PointerEventsBoxNone, StyleFunc: func() core.Style {
				return core.Style{Width: core.Pct(100), MaxWidth: core.Pt(440), Gap: 14, CornerRadius: 26,
					Padding: core.Edges{Top: 18, Right: 20, Bottom: 14, Left: 20}, Background: colors.Canvas()}
			}, Children: []core.Node{
				core.View{PointerEvents: core.PointerEventsNone, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10}, Children: []core.Node{
					nswidgets.Icon("microphone", 18, colors.Link),
					core.Text{TestID: "voice-status", ContentFunc: live(m, func() string {
						v := m.voice
						switch {
						case v == nil:
							return ""
						case v.released && v.autoSend:
							return tr("Recognizing… Will send when ready")
						case v.released:
							return tr("Recognizing…")
						case v.autoSend:
							return tr("Listening… Release to send")
						default:
							return tr("Listening… Release to finish")
						}
					}), StyleFunc: func() core.Style { return core.Style{FontSize: 13, Color: colors.Muted(), FlexShrink: 1} }},
				}},
				core.View{PointerEvents: core.PointerEventsNone, StyleFunc: func() core.Style {
					return core.Style{MinHeight: core.Pt(33), MaxHeight: core.Pt(max(120, cx.Env().Window().Height*0.4)),
						Overflow: core.Hidden, JustifyContent: core.JustifyEnd}
				}, Children: []core.Node{core.Text{TestID: "voice-text", ContentFunc: live(m, func() string {
					if m.voice == nil || m.voice.partial == "" {
						return "…"
					}
					return m.voice.partial
				}), StyleFunc: func() core.Style { return core.Style{FontSize: 22, FontWeight: 500, Color: colors.Ink()} }}}},
				core.Show{When: released, Then: func(cx *core.Scope) core.Node {
					return core.View{PointerEvents: core.PointerEventsBoxNone, Style: core.Style{AlignItems: core.AlignEnd}, Children: []core.Node{
						ui.Button{TestID: "voice-cancel", Text: tr("Cancel"), Variant: ui.ButtonGhost,
							Style: core.Style{MinHeight: core.Pt(44)}, OnPress: m.act(m.cancelVoice)},
					}}
				}},
			}}},
		}
	}}
}

func (m *App) voiceSettings(cx *core.Scope) core.Node {
	return m.groupedPage(cx, m.group(cx, "", groupedFooter(cx, "", func() string {
		return tr("Hold the microphone button to speak. Release to insert text, or enable automatic sending.")
	}), gRow{Label: tr("Send when done speaking"), Switch: &ui.Switch{TestID: "voice-auto-send", Label: tr("Send when done speaking"),
		Checked: live(m, func() bool { return m.voiceAutoSend }), OnChange: func(on bool) {
			m.voiceAutoSend = on
			value := "off"
			if on {
				value = "on"
			}
			prefs.Save("phone-voice-auto-send", value)
			m.change()
		}}}))
}
