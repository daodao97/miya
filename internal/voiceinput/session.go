package voiceinput

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/microphone"
	"github.com/egoist/mygo/plugins/speech"
)

// ProviderName is the speech (plugins/speech) provider that recognizes speech through
// WeChat's cloud service.
const ProviderName = "wechat"

// Provider captures with plugins/microphone and streams the audio to the cloud.
// Dir keeps the service's device identity.
type Provider struct{ Dir string }

var register struct {
	once sync.Once
	err  error
}

// Start begins a WeChat recognition session, registering the provider on
// first use; dir is fixed by the first call.
func Start(ctx context.Context, dir string) (*speech.Session, error) {
	register.once.Do(func() { register.err = speech.RegisterProvider(ProviderName, Provider{Dir: dir}) })
	if register.err != nil {
		return nil, register.err
	}
	return speech.Start(ctx, speech.Options{Provider: ProviderName})
}

func (p Provider) Start(ctx context.Context, _ speech.Options, emit func(speech.Result)) (speech.Recording, error) {
	// speech.Start ends capture after its MaxDuration; this bounds the cloud
	// round trips that follow.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	r := &recording{audio: make(chan []byte, 640), cancel: cancel}
	mic, err := microphone.Start(ctx, r.capture)
	if err != nil {
		cancel()
		return nil, err
	}
	r.mic = mic
	go func() {
		defer cancel()
		defer mic.Cancel()
		text, err := recognizeWechat(ctx, p.Dir, r.audio, func(text string) { emit(speech.Result{Text: text}) })
		if captureError := r.captureErr(); captureError != nil {
			err = captureError
		}
		emit(speech.Result{Text: text, Final: true, Err: err})
	}()
	return r, nil
}

// recording hands microphone audio to the recognizer. Stop ends capture and
// waits for the cloud's final text; Cancel discards the recording at once.
type recording struct {
	mu           sync.Mutex
	audio        chan []byte
	captureError error
	closed       bool
	mic          *microphone.Capture
	cancel       context.CancelFunc
	stopOnce     sync.Once
}

func (r *recording) Stop() {
	r.stopOnce.Do(func() {
		r.mic.Stop()
		// Also bound finalization when release precedes the cloud handshake.
		time.AfterFunc(20*time.Second, r.cancel)
	})
}

func (r *recording) Cancel() { r.cancel(); r.mic.Cancel() }

func (r *recording) captureErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.captureError
}

// capture runs on the audio thread; it never blocks.
func (r *recording) capture(c microphone.Chunk) {
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
