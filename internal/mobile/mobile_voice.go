package mobile

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/permissionflow"
	"github.com/egoist/mygo/plugins/speech"
	"github.com/egoist/mygo/ui"

	"miya/internal/prefs"
	"miya/internal/uiruntime"
	"miya/internal/voiceinput"
	"miya/internal/widgets"
)

const voiceAutoSendPreference = "phone-voice-auto-send"

type mobileVoiceSession interface {
	Results() <-chan speech.Result
	Stop()
	Cancel()
}

type mobileVoice struct {
	cancel                context.CancelFunc
	session               mobileVoiceSession
	owner, draft, partial string
	anchor, caret         int
	released              bool
	autoSend              bool
	final                 *speech.Result
}

// Touch arbitration, the hold threshold and pointer capture belong to MyGo's
// shared UI. Both phone platforms deliver the same events here.
func (m *App) voiceInput(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputLongPress:
		m.beginVoice()
		return true
	case ui.InputPointerUp:
		if m.voice != nil {
			if ev.Canceled {
				m.cancelVoice()
			} else {
				m.endVoice()
			}
			return true
		}
	}
	return false
}

func (m *App) voiceOwner() string {
	return fmt.Sprintf("%s:%d:%s:%d:%s", m.Link, m.Generation, m.ComposerProject(), m.Open, m.composerAgent())
}

func (m *App) beginVoice() {
	if m.voice != nil || m.Sending || !m.voicePage() || m.Menu || m.ConversationMenu {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	v := &mobileVoice{cancel: cancel, owner: m.voiceOwner(), draft: m.Draft, autoSend: m.VoiceAutoSend}
	if doc := m.InlineInput.Doc; doc != nil {
		v.anchor, v.caret = doc.Selection()
	}
	m.voice = v
	m.SendErr = ""
	start := m.voiceStart
	dir := m.DataDir
	if start == nil {
		start = func(ctx context.Context) (mobileVoiceSession, error) { return voiceinput.Start(ctx, dir) }
	}
	// Capture starts on the touch's UI thread; the cloud runs in Session's
	// worker. MyGo permission prompts pump native events, so a release during
	// authorization can cancel ctx before the microphone is opened.
	session, err := start(ctx)
	if m.voice != v || ctx.Err() != nil {
		if session != nil {
			session.Cancel()
		}
		return
	}
	if err != nil {
		m.finishVoice(v, speech.Result{Err: err, Final: true})
		return
	}
	if session == nil {
		m.cancelVoice()
		return
	}
	v.session = session
	mobileFeedback(mygo.Haptics.Light)
	go func() {
		for r := range session.Results() {
			uiruntime.RunOnMain(func() {
				if m.voice != v {
					return
				}
				if r.Final {
					if v.released || r.Err != nil {
						m.finishVoice(v, r)
					} else {
						v.final = &r
						v.partial = r.Text
					}
				} else {
					v.partial = r.Text
				}
			})
		}
	}()
}

func (m *App) endVoice() {
	if v := m.voice; v != nil && !v.released {
		v.released = true
		if v.final != nil {
			m.finishVoice(v, *v.final)
			return
		}
		if v.session == nil {
			// A permission sheet may have consumed the release. Never begin
			// recording after the user's finger is already off the input.
			m.cancelVoice()
			return
		}
		v.session.Stop()
		mobileFeedback(mygo.Haptics.Light)
	}
}

func (m *App) cancelVoice() {
	if v := m.voice; v != nil {
		m.voice = nil
		v.cancel()
		if v.session != nil {
			v.session.Cancel()
		}
	}
}

func (m *App) finishVoice(v *mobileVoice, r speech.Result) {
	if m.voice != v {
		return
	}
	m.voice = nil
	v.cancel()
	if v.session != nil {
		v.session.Cancel()
	}
	if v.owner != m.voiceOwner() || !m.voicePage() {
		return
	}
	if r.Err != nil {
		m.SendErr = tr("Voice recognition failed. Check your network and microphone permission, then try again.")
		return
	}
	text := strings.TrimSpace(r.Text)
	if text == "" {
		m.SendErr = tr("No speech recognized. Press and hold to try again.")
		return
	}
	if m.Draft != v.draft {
		m.SendErr = tr("The draft changed during recording. Please try again.")
		return
	}
	if doc := m.InlineInput.Doc; doc != nil {
		doc.SetSelection(v.anchor, v.caret)
		m.Draft = m.InlineInput.InsertText(text)
	} else {
		m.Draft += text
	}
	mobileFeedback(mygo.Haptics.Success)
	if v.autoSend && v.released {
		m.Send()
	}
}

func (m *App) voicePage() bool {
	path := m.Router().Path()
	return m.Loaded && m.Link != "" && (path == "/" || path == "/thread")
}

// voiceStatus shows what is being recognized in a large card over the dimmed
// page, so the words can be read at a glance while the finger stays on the
// input. The touch stays captured by the input, so the overlay never takes
// the release.
func (m *App) voiceStatus(c *ui.Context) {
	v := m.voice
	if v == nil {
		return
	}
	p := m.Pal
	label := tr("Listening… Release to finish")
	if v.autoSend {
		label = tr("Listening… Release to send")
	}
	if v.released {
		label = tr("Recognizing…")
		if v.autoSend {
			label = tr("Recognizing… Will send when ready")
		}
	}
	root := c.Root().Bounds()
	safe := safeAreaOf(c)
	scrim := ui.RGBA(0, 0, 0, 0.35)
	if m.Dark {
		scrim = ui.RGBA(0, 0, 0, 0.6)
	}
	ui.Overlay(c, func() {
		ui.Column(c.Key("voice-overlay")).Absolute().Left(0).Top(0).Width(root.W).Height(root.H).
			Padding(safe.Top+24, safe.Right+24, safe.Bottom+24, safe.Left+24).
			Background(scrim).PassThrough().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			card := ui.Column(c.Key("voice-card")).FillWidth().MaxWidth(440).Padding(18, 20, 14, 20).Gap(14).
				Background(p.Bg).Border(1, p.line).Radius(26).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.25))
			card.Children(func() {
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					badge := p.link
					if v.released {
						badge = p.muted
					}
					ui.Row(c).Size(28, 28).Shrink(0).Radius(14).Background(badge.Alpha(0.15)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
						widgets.Icon(c, "microphone", 16, badge)
					})
					ui.Text(c, label).FontSize(m.Font(13)).TextColor(p.muted).Grow(1).MinWidth(0).Wrap()
				})
				// The newest words stay in view: a long dictation overflows
				// upward and is clipped at the top.
				body := ui.Column(c.Key("voice-text")).FillWidth().MinHeight(m.Font(22) * 1.5).MaxHeight(max(120, root.H*0.4)).Clip().Justify(ui.End)
				body.Children(func() {
					if v.partial != "" {
						ui.Text(c, v.partial).FontSize(m.Font(22)).FontWeight(500).TextColor(p.ink).FillWidth().Wrap()
					} else {
						ui.Text(c, "…").FontSize(m.Font(22)).TextColor(p.muted)
					}
				})
				if v.released {
					ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
						if m.phoneButton(c.Key("voice-cancel")).MinHeight(44).Label(tr("Cancel voice input")).Children(func() {
							ui.Text(c, tr("Cancel")).FontSize(m.Font(15)).TextColor(p.link)
						}).Clicked() {
							m.cancelVoice()
						}
					})
				}
			})
		})
	})
}

func (m *App) voiceSettings(c *ui.Context) {
	help := tr("Hold the input to speak. Release to insert the recognized text without sending.")
	if m.VoiceAutoSend {
		help = tr("Hold the input to speak. Release to recognize and send automatically.")
	}
	m.settingsGroup(c, "", help, func() {
		ui.Row(c).FillWidth().MinHeight(52).Padding(12, 16).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("WeChat cloud recognition")).FontSize(m.Font(17)).Grow(1).MinWidth(0).Wrap()
			widgets.Icon(c, "check", 18, m.Pal.link)
		})
		m.settingsSeparator(c)
		if m.settingsAction(c.Key("voice-authorize"), tr("Allow microphone")).Clicked() {
			go func() {
				status, err := permissionflow.Request(context.Background(), permissionflow.Microphone)
				uiruntime.RunOnMain(func() {
					if err != nil || status != permissionflow.Granted {
						m.voicePermission = tr("Allow microphone access in system settings to use voice input.")
					} else {
						m.voicePermission = tr("Microphone access allowed")
					}
				})
			}()
		}
		m.settingsSeparator(c)
		ui.Row(c).FillWidth().MinHeight(52).Padding(4, 16).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Send when done speaking")).FontSize(m.Font(17)).Grow(1).MinWidth(0).Wrap()
			if m.PhoneSwitch(c.Key("voice-auto-send"), &m.VoiceAutoSend, tr("Enable sending after voice recognition")).Changed() {
				m.setVoiceAutoSend(m.VoiceAutoSend)
			}
		})
	})
	if m.voicePermission != "" {
		ui.Text(c, m.voicePermission).FontSize(m.Font(13)).Wrap().TextColor(m.Pal.muted).PaddingX(16)
	}
}

func (m *App) setVoiceAutoSend(enabled bool) {
	m.VoiceAutoSend = enabled
	value := "off"
	if enabled {
		value = "on"
	}
	prefs.Save(voiceAutoSendPreference, value)
}
