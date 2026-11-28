package nsmobile

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/microphone"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/chat/composer"
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
	draft              composer.DictationDraft
	partial            string
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
func (m *App) voicePage() bool {
	return m.Connected && (m.Path == "/" || m.Path == "/thread") && m.pages.Top() == ""
}
func (m *App) voiceValid(v *phoneVoice) bool {
	return m.voice == v && v.owner == m.composerOwner() && m.voicePage()
}
func (m *App) beginVoice() {
	if m.composing || m.voice != nil || m.Sending || !m.voicePage() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	v := &phoneVoice{cancel: cancel, owner: m.composerOwner(), draft: composer.NewDictationDraft(m.Draft, m.anchor, m.caret), autoSend: m.voiceAutoSend}
	m.voice, m.SendErr = v, ""
	m.cx.DismissKeyboard()
	start, dir := m.voiceStart, m.DataDir
	if start == nil {
		start = func(ctx context.Context, dir string, emit func(voiceinput.Result)) (voiceSession, error) {
			return voiceinput.Start(ctx, dir, func(rate int, samples func([]int16)) (voiceinput.CaptureStream, error) {
				return microphone.Capture(rate, samples)
			}, emit)
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
	if m.Draft != v.draft.Preview {
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
	m.setComposerDraft(v.draft.Replace(s))
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
		if v.owner.link == m.Link && v.owner.epoch == m.ComposerEpoch() && m.Draft == v.draft.Preview {
			m.setComposerDraft(v.draft.Original, v.draft.Start, v.draft.End)
		}
	}
}
func (m *App) finishVoice(v *phoneVoice, r voiceinput.Result) {
	if !m.voiceValid(v) {
		m.cancelVoice()
		return
	}
	if m.Draft != v.draft.Preview {
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

// voiceHold shares one recording gesture between the editor and the right
// button. Lifting finishes the recording and sliding away cancels it.
func (m *App) voiceHold(cx *core.Scope, quiet *time.Time) func(core.TouchEvent, bool) {
	var stop func() bool
	var origin core.Point
	var started bool
	stopTimer := func() {
		if stop != nil {
			stop()
			stop = nil
		}
	}
	finish := func(end func()) {
		stopTimer()
		if started {
			started = false
			*quiet = time.Now().Add(400 * time.Millisecond)
			end()
			m.change()
		}
	}
	cx.Cleanup(func() { stopTimer(); m.cancelVoice() })
	return func(e core.TouchEvent, allowStart bool) {
		switch e.Phase {
		case core.TouchStart:
			if len(e.Touches) != 1 {
				finish(m.cancelVoice)
				return
			}
			stopTimer()
			if allowStart && m.voicePage() {
				origin = e.Touches[0].PagePosition
				owner := m.composerOwner()
				stop = cx.AfterFunc(350*time.Millisecond, func() {
					stop = nil
					if owner == m.composerOwner() && m.voice == nil {
						m.beginVoice()
						started = m.voice != nil
						m.change()
					}
				})
			}
		case core.TouchMove:
			limit := core.Points(12)
			if started {
				limit = 60
			}
			for _, t := range e.Touches {
				if dx, dy := t.PagePosition.X-origin.X, t.PagePosition.Y-origin.Y; dx*dx+dy*dy > limit*limit {
					finish(m.cancelVoice)
				}
			}
		case core.TouchEnd:
			finish(m.endVoice)
		case core.TouchCancel:
			finish(m.cancelVoice)
		}
	}
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
		return tr("Hold the microphone or the empty input to speak. Release to insert text, or enable automatic sending.")
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
