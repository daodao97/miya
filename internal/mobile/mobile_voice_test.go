package mobile

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/speech"
	"github.com/egoist/mygo/ui"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/uiruntime"
)

type fakeVoice struct {
	stopped, canceled bool
	results           chan speech.Result
}

func (f *fakeVoice) Stop()                         { f.stopped = true }
func (f *fakeVoice) Cancel()                       { f.canceled = true }
func (f *fakeVoice) Results() <-chan speech.Result { return f.results }

func voiceFixture(t *testing.T) (*App, *ui.Tester, *mobileVoice, *fakeVoice) {
	t.Helper()
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	m := NewMobileApp()
	m.Loaded, m.Link, m.Connected = true, "miya://test", true
	m.State = phoneproto.State{Projects: []model.ProjectSnapshot{{Name: "Project", Path: "/project", Current: true}}}
	m.Draft = "开始👋尾巴"
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	m.InlineInput.Doc.SetSelection(3, 5)
	_, cancel := context.WithCancel(context.Background())
	f := &fakeVoice{}
	v := &mobileVoice{cancel: cancel, session: f, owner: m.voiceOwner(), draft: m.Draft, anchor: 3, caret: 5}
	m.voice = v
	t.Cleanup(m.cancelVoice)
	return m, tt, v, f
}

func TestVoiceReleaseWaitsAndInsertsAtSelectionWithoutSending(t *testing.T) {
	m, tt, v, f := voiceFixture(t)
	m.endVoice()
	if !f.stopped || m.voice == nil || m.Draft != v.draft {
		t.Fatal("release must stop capture and retain the draft while finalizing")
	}
	tt.Frame()
	if !tt.HasText("Recognizing…") {
		t.Fatal("missing finalization state")
	}
	saveVoiceSnapshot(t, tt, "voice-recognizing")
	m.Send()
	if m.SendErr != "" || m.Sending {
		t.Fatal("send should be ignored while recognizing")
	}
	m.finishVoice(v, speech.Result{Text: "识别结果", Final: true})
	tt.Frame()
	if m.Draft != "开始👋识别结果" || m.voice != nil || !f.canceled || len(m.Outbox) != 0 || m.Sending || m.SendErr != "" {
		t.Fatalf("unexpected final state: draft=%q", m.Draft)
	}
	// The dictated replacement remains one ordinary editor undo step.
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if m.Draft != v.draft {
		t.Fatalf("undo lost original draft: %q", m.Draft)
	}
}

func TestVoiceLateResultCannotEnterAnotherConversation(t *testing.T) {
	m, _, v, f := voiceFixture(t)
	v.autoSend, v.released = true, true
	m.Open = 42
	m.Draft = "other conversation"
	m.finishVoice(v, speech.Result{Text: "late result", Final: true})
	if m.Draft != "other conversation" || !f.canceled || m.SendErr != "" {
		t.Fatal("late result changed another conversation")
	}
}

func TestVoiceCancelAndFailureKeepDraft(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancel"}[cancel], func(t *testing.T) {
			m, _, v, f := voiceFixture(t)
			v.autoSend, v.released = true, true
			if cancel {
				m.cancelVoice()
			} else {
				m.finishVoice(v, speech.Result{Final: true, Err: errors.New("offline")})
			}
			if m.Draft != v.draft || m.voice != nil || !f.canceled || m.SendErr == tr("Not connected to desktop") {
				t.Fatal("failed/canceled recognition changed draft or retained capture")
			}
			m.finishVoice(v, speech.Result{Text: "late", Final: true})
			if m.Draft != v.draft {
				t.Fatal("canceled result was inserted")
			}
		})
	}
}

func TestVoiceReleaseDuringPermissionDoesNotStartLater(t *testing.T) {
	m, _, v, _ := voiceFixture(t)
	v.session = nil
	m.endVoice()
	if m.voice != nil || m.Draft != v.draft {
		t.Fatal("release during permission must cancel")
	}
}

func TestVoiceNavigationCancelsCapture(t *testing.T) {
	m, tt, _, f := voiceFixture(t)
	m.OpenDrawerPage("/settings")
	tt.Frame()
	if m.voice != nil || !f.canceled {
		t.Fatal("navigation retained microphone capture")
	}
}

func TestVoiceUsesSharedInputEvents(t *testing.T) {
	m, _, _, _ := voiceFixture(t)
	m.cancelVoice()
	f := &fakeVoice{results: make(chan speech.Result)}
	m.voiceStart = func(context.Context) (mobileVoiceSession, error) { return f, nil }
	if !m.voiceInput(ui.InputEvent{Kind: ui.InputLongPress}) || m.voice == nil {
		t.Fatal("shared long press did not start capture")
	}
	if m.voiceInput(ui.InputEvent{Kind: ui.InputText, Text: "x"}) {
		t.Fatal("voice consumed ordinary text input")
	}
	if !m.voiceInput(ui.InputEvent{Kind: ui.InputPointerUp}) || !f.stopped {
		t.Fatal("shared release did not finalize capture")
	}
	if !m.voiceInput(ui.InputEvent{Kind: ui.InputPointerUp, Canceled: true}) || !f.canceled || m.voice != nil {
		t.Fatal("shared cancellation did not release capture")
	}
	close(f.results)
}

func TestVoiceHoldOnlyBeforeComposerFocus(t *testing.T) {
	m, tt, _, _ := voiceFixture(t)
	m.cancelVoice()
	f := &fakeVoice{results: make(chan speech.Result)}
	defer close(f.results)
	starts := 0
	m.voiceStart = func(context.Context) (mobileVoiceSession, error) {
		starts++
		return f, nil
	}
	tt.UsePointer(ui.PointerTouch, 1)
	hold := func() {
		t.Helper()
		bounds, ok := tt.Find("Message")
		if !ok {
			t.Fatal("missing composer")
		}
		x, y := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
		tt.Press(x, y)
		tt.Advance(time.Second)
		tt.Release(x, y)
	}
	hold()
	if starts != 1 || m.voice == nil || !f.stopped || tt.Focused("Message") {
		t.Fatal("an unfocused hold must capture voice without opening the editor")
	}
	m.cancelVoice()
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	if !tt.Focused("Message") {
		t.Fatal("a light tap must focus the editor")
	}
	hold()
	if starts != 1 || m.voice != nil || !tt.Focused("Message") {
		t.Fatal("holding the focused editor must preserve text editing without recording")
	}
	tt.Type(" typed")
	if !strings.Contains(m.Draft, " typed") {
		t.Fatal("focused editor no longer accepts text")
	}
	tt.ClickAt(195, 200)
	if tt.Focused("Message") {
		t.Fatal("tapping outside must leave editing")
	}
	hold()
	if starts != 2 || m.voice == nil {
		t.Fatal("voice hold must be available again after the editor loses focus")
	}
}

func TestVoiceCloudFinalWaitsForFingerRelease(t *testing.T) {
	m, _, _, _ := voiceFixture(t)
	m.cancelVoice()
	m.VoiceAutoSend = true
	posts := make(chan func(), 10)
	previous := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { posts <- f }
	t.Cleanup(func() { uiruntime.RunOnMain = previous })
	f := &fakeVoice{results: make(chan speech.Result, 2)}
	m.voiceStart = func(context.Context) (mobileVoiceSession, error) { return f, nil }
	next := func() {
		t.Helper()
		select {
		case work := <-posts:
			work()
		case <-time.After(time.Second):
			t.Fatal("missing UI callback")
		}
	}
	m.beginVoice()
	if !m.voice.autoSend {
		t.Fatal("recording did not capture the auto-send preference")
	}
	// A recording uses the preference at its start, rather than changing
	// behavior midway through the user's gesture.
	m.VoiceAutoSend = false
	before := m.Draft
	f.results <- speech.Result{Text: "云端定稿", Final: true}
	close(f.results)
	next()
	if m.Draft != before || m.voice == nil || m.voice.final == nil || m.SendErr != "" {
		t.Fatal("cloud final was inserted before release")
	}
	m.endVoice()
	if m.voice != nil || m.Draft == before || !f.canceled || m.SendErr != tr("Not connected to desktop") {
		t.Fatal("release did not commit cloud final")
	}
}

func TestVoiceAutoSendUsesNormalSendAndKeepsOfflineDraft(t *testing.T) {
	m, _, v, f := voiceFixture(t)
	v.autoSend, v.released = true, true
	m.finishVoice(v, speech.Result{Text: "识别结果", Final: true})
	if m.SendErr != tr("Not connected to desktop") || m.Draft != "开始👋识别结果" || m.voice != nil || !f.canceled || m.Sending {
		t.Fatalf("auto-send must use the normal offline send path and retain text: draft=%q error=%q", m.Draft, m.SendErr)
	}
	m.SendErr = "later status"
	m.finishVoice(v, speech.Result{Text: "duplicate", Final: true})
	if m.Draft != "开始👋识别结果" || m.SendErr != "later status" {
		t.Fatal("a duplicate final triggered a second send attempt")
	}
}

func TestVoiceAutoSendRejectsEmptyRecognitionAndChangedDraft(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "changed draft"}[changed], func(t *testing.T) {
			m, _, v, _ := voiceFixture(t)
			v.autoSend, v.released = true, true
			text := ""
			if changed {
				m.Draft, text = "edited draft", "识别结果"
			}
			before := m.Draft
			m.finishVoice(v, speech.Result{Text: text, Final: true})
			if m.Draft != before || m.voice != nil || m.SendErr == "" || m.SendErr == tr("Not connected to desktop") {
				t.Fatalf("invalid voice result attempted sending: draft=%q error=%q", m.Draft, m.SendErr)
			}
		})
	}
}

func TestVoiceAutoSendSwitchPersistsOnThisDevice(t *testing.T) {
	m, tt, _, _ := voiceFixture(t)
	m.cancelVoice()
	if !m.VoiceAutoSend {
		t.Fatal("auto-send must default to on")
	}
	m.Router().Push("/settings/voice")
	tt.Advance(time.Second)
	saveVoiceSnapshot(t, tt, "voice-auto-send-on")
	if err := tt.Click("Enable sending after voice recognition"); err != nil {
		t.Fatal(err)
	}
	if m.VoiceAutoSend || NewMobileApp().VoiceAutoSend || !tt.HasText("Hold the input to speak. Release to insert the recognized text without sending.") {
		t.Fatalf("auto-send switch: enabled=%v saved=%v help=%v", m.VoiceAutoSend, NewMobileApp().VoiceAutoSend, tt.HasText("Hold the input to speak. Release to insert the recognized text without sending."))
	}
	tt.SetLocale("zh-Hans")
	tt.Advance(200 * time.Millisecond)
	saveVoiceSnapshot(t, tt, "voice-auto-send-off-zh")
	tt.SetDark(true)
	tt.Advance(200 * time.Millisecond)
	saveVoiceSnapshot(t, tt, "voice-auto-send-off-dark")
	tt.SetDark(false)
	tt.SetLocale("en")
	tt.SetSize(320, 844)
	tt.SetPreferences(ui.Preferences{TextScale: 2})
	control, ok := tt.Find("Enable sending after voice recognition")
	if !ok || control.W < 44 || control.H < 44 || control.X < 16 || control.X+control.W > 304 {
		t.Fatalf("auto-send switch leaves the narrow screen at large text size: %v", control)
	}
	saveVoiceSnapshot(t, tt, "voice-auto-send-large")
	tt.SetSize(390, 750)
	tt.SetPreferences(ui.Preferences{TextScale: 1})
	if err := tt.Click("Enable sending after voice recognition"); err != nil {
		t.Fatal(err)
	}
	if !m.VoiceAutoSend || !NewMobileApp().VoiceAutoSend || !tt.HasText("Hold the input to speak. Release to recognize and send automatically.") {
		t.Fatal("turning auto-send back on did not restore automatic sending")
	}
}

func saveVoiceSnapshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("MIYA_SNAPSHOT"); dir != "" {
		file, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := png.Encode(file, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}
