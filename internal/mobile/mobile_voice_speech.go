package mobile

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/microphone"
	"github.com/egoist/mygo/plugins/speech"

	"miya/internal/voiceinput"
)

// The MyGo phone records through MyGo's speech plugin; the recognizer is
// voiceinput's, which the NSGo apps drive through NSGo's microphone.

// speechProviderName is the speech provider that recognizes speech through
// WeChat's cloud service.
const speechProviderName = "wechat"

// speechProvider captures with plugins/microphone and streams the audio to
// the cloud. dir keeps the service's device identity.
type speechProvider struct{ dir string }

var speechRegister struct {
	once sync.Once
	err  error
}

// startSpeech begins a WeChat recognition session, registering the provider
// on first use; dir is fixed by the first call.
func startSpeech(ctx context.Context, dir string) (*speech.Session, error) {
	speechRegister.once.Do(func() { speechRegister.err = speech.RegisterProvider(speechProviderName, speechProvider{dir: dir}) })
	if speechRegister.err != nil {
		return nil, speechRegister.err
	}
	return speech.Start(ctx, speech.Options{Provider: speechProviderName})
}

func (p speechProvider) Start(ctx context.Context, _ speech.Options, emit func(speech.Result)) (speech.Recording, error) {
	// speech.Start ends capture after its MaxDuration; this bounds the cloud
	// round trips that follow.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	r := &speechRecording{audio: make(chan []byte, 640), cancel: cancel}
	mic, err := microphone.Start(ctx, r.capture)
	if err != nil {
		cancel()
		return nil, err
	}
	r.mic = mic
	go func() {
		defer cancel()
		defer mic.Cancel()
		text, err := voiceinput.Recognize(ctx, p.dir, r.audio, func(text string) { emit(speech.Result{Text: text}) })
		if captureError := r.captureErr(); captureError != nil {
			err = captureError
		}
		emit(speech.Result{Text: text, Final: true, Err: err})
	}()
	return r, nil
}

// speechRecording hands microphone audio to the recognizer. Stop ends
// capture and waits for the cloud's final text; Cancel discards the
// recording at once.
type speechRecording struct {
	mu           sync.Mutex
	audio        chan []byte
	captureError error
	closed       bool
	mic          *microphone.Capture
	cancel       context.CancelFunc
	stopOnce     sync.Once
}

func (r *speechRecording) Stop() {
	r.stopOnce.Do(func() {
		r.mic.Stop()
		// Also bound finalization when release precedes the cloud handshake.
		time.AfterFunc(20*time.Second, r.cancel)
	})
}

func (r *speechRecording) Cancel() { r.cancel(); r.mic.Cancel() }

func (r *speechRecording) captureErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.captureError
}

// capture runs on the audio thread; it never blocks.
func (r *speechRecording) capture(c microphone.Chunk) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	err, end := c.Err, c.End
	if len(c.PCM) > 0 {
		select {
		case r.audio <- c.PCM:
		default:
			err, end = errors.New("voice audio buffer full"), true
		}
	}
	if err != nil || end {
		r.captureError = err
		r.closed = true
		close(r.audio)
	}
}
