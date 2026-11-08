//go:build miya_ios_input_test

package mobile

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/uiruntime"
)

// Native input tests use the real phone page and composer, without a desktop
// connection or the ability to send a message. This entry is never shipped.
func MobileEntry() {
	m := NewMobileApp()
	m.Loaded, m.Link, m.Host = true, "miya://input-fixture", tr("Input test")
	source := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	thread := model.Thread{ID: 1, ServerID: "input-fixture", Agent: agentmeta.Default().ID, Title: tr("Native input interaction test"), HistoryLoaded: true}
	m.State = phoneproto.State{ReferenceSource: source, Projects: []model.ProjectSnapshot{{Name: "Miya", Path: "/input-fixture", Current: true, Threads: []model.Thread{thread}}}}
	m.Router().Transition = ui.TransitionNone
	m.OpenThread(thread)
	m.ShowThread(thread)
	m.Draft = os.Getenv("MIYA_INPUT_SAMPLE")
	if m.Draft == "" {
		m.Draft = "description"
	}
	if os.Getenv("MIYA_INPUT_VOICE_TEST") == "1" {
		// Test UIKit hold arbitration without microphone permissions or
		// uploading audio. The cloud path has a separate opt-in live test.
		m.voiceStart = newInputFixtureVoice
	}
	mygo.App.WhenReady(func() {
		m.win = mygo.NewWindow(mygo.WindowOptions{Title: tr("Miya Input Tests"), Width: 390, Height: 844, ExtendIntoSafeArea: true, Content: ui.View(func(c *ui.Context) {
			m.ViewRoot(c)
			// Expose editor commands in this test harness so native selection
			// and clipboard tests do not depend on an attached hardware keyboard.
			// This control is never part of the production composer.
			if m.InlineInput.EditMenu != nil {
				ui.ButtonBase(c.Key("test-edit")).Absolute().Top(120).Right(12).MinHeight(44).Padding(8).Label(tr("Edit text")).Children(func() {
					ui.Text(c, tr("Edit text"))
				}).PopoverMenuButton(m.InlineInput.EditMenu)
			}
		})})
		uiruntime.SetAppWindow(m.win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

type inputFixtureVoice struct {
	results chan mygo.SpeechResult
	stop    chan struct{}
	once    sync.Once
	cancel  context.CancelFunc
	started time.Time
}

func (s *inputFixtureVoice) Stop()                             { s.once.Do(func() { close(s.stop) }) }
func (s *inputFixtureVoice) Cancel()                           { s.cancel() }
func (s *inputFixtureVoice) Results() <-chan mygo.SpeechResult { return s.results }
func newInputFixtureVoice(ctx context.Context) (mobileVoiceSession, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &inputFixtureVoice{results: make(chan mygo.SpeechResult, 1), stop: make(chan struct{}), cancel: cancel, started: time.Now()}
	go func() {
		defer close(s.results)
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
		}
		if time.Since(s.started) < 250*time.Millisecond {
			s.results <- mygo.SpeechResult{Err: errors.New("capture began after release"), Final: true}
			return
		}
		timer := time.NewTimer(300 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		select {
		case <-ctx.Done():
		case s.results <- mygo.SpeechResult{Text: "voice transcription test", Final: true}:
		}
	}()
	return s, nil
}
