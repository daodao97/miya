package nsmobile

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/microphone"
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
	return m.voice == v && v.owner == m.composerOwner() && m.Connected && m.Path == "/thread" && m.sheet.Get() == ""
}
func (m *App) beginVoice() {
	if m.voice != nil || m.Sending || !m.Connected || m.Path != "/thread" || m.sheet.Get() != "" {
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
func (m *App) voiceAccessory(cx *core.Scope) core.Node {
	var stop func() bool
	var down, touch bool
	var origin core.Point
	cancelHold := func() {
		if stop != nil {
			stop()
			stop = nil
		}
		down = false
	}
	begin := func() {
		if down {
			return
		}
		down = true
		stop = cx.AfterFunc(350*time.Millisecond, m.act(m.beginVoice))
	}
	release := func() { cancelHold(); m.endVoice(); m.change() }
	cancel := func() { cancelHold(); m.cancelVoice(); m.change() }
	cx.Cleanup(func() { cancelHold(); m.cancelVoice() })
	return column(core.View{TestID: "voice-hold", Label: tr("Hold to speak"), Role: core.RoleButton,
		Style:       core.Style{MinHeight: core.Pt(44), Padding: core.All(10)},
		Disabled:    live(m, func() bool { return !m.Connected || m.Sending }),
		OnPressDown: func(core.PressEvent) { begin() }, OnPressUp: func(core.PressEvent) {
			if !touch {
				release()
			}
		},
		OnTouch: func(e core.TouchEvent) {
			switch e.Phase {
			case core.TouchStart:
				if len(e.Touches) != 1 {
					cancel()
					return
				}
				touch = true
				origin = e.Touches[0].PagePosition
				begin()
			case core.TouchMove:
				for _, t := range e.Touches {
					dx, dy := t.PagePosition.X-origin.X, t.PagePosition.Y-origin.Y
					if dx*dx+dy*dy > 60*60 {
						cancel()
					}
				}
			case core.TouchEnd:
				release()
				touch = false
			case core.TouchCancel:
				cancel()
				touch = false
			}
		},
		Responder: &core.Responder{Claims: []core.TouchClaim{{}}, Keep: true, OnTerminate: func(core.ResponderEvent) { cancel(); touch = false }},
		Children:  []core.Node{text(tr("Hold to speak"))}},
		core.Show{When: live(m, func() bool { return m.voice != nil }), Then: func(cx *core.Scope) core.Node {
			return column(core.Text{TestID: "voice-status", ContentFunc: live(m, func() string {
				if v := m.voice; v != nil {
					if v.released {
						if v.autoSend {
							return tr("Recognizing… Will send when ready")
						}
						return tr("Recognizing…")
					}
					if v.autoSend {
						return tr("Listening… Release to send")
					}
					return tr("Listening… Release to finish")
				}
				return ""
			})}, text(tr("Slide away to cancel")), m.button("voice-cancel", tr("Cancel voice input"), nil, cancel))
		}})
}
func (m *App) voiceSettings(cx *core.Scope) core.Node {
	return scroll(text(tr("Voice input")), text(tr("Hold the microphone button to speak. Release to insert text, or enable automatic sending.")),
		core.View{TestID: "voice-auto-send", Role: core.RoleButton, Label: tr("Send when done speaking"), OnPress: func(core.PressEvent) {
			m.voiceAutoSend = !m.voiceAutoSend
			value := "off"
			if m.voiceAutoSend {
				value = "on"
			}
			prefs.Save("phone-voice-auto-send", value)
			m.change()
		}, Style: core.Style{MinHeight: core.Pt(44)}, Children: []core.Node{core.Text{ContentFunc: live(m, func() string {
			s := tr("Send when done speaking")
			if m.voiceAutoSend {
				s = "✓ " + s
			}
			return s
		})}}},
		m.button("voice-done", tr("Done"), nil, func() { m.sheet.Set("") }))
}
