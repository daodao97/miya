package voiceinput

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/microphone"
)

// SampleRate is the audio the recognizers take: mono signed 16-bit
// little-endian PCM at 16 kHz.
const SampleRate = 16000

// MaxDuration bounds one recording; recognition of what was said then
// still finishes.
const MaxDuration = 60 * time.Second

// Result is recognized text: partial while the user speaks, then once
// Final, with Err when recognition or capture failed.
type Result struct {
	Text  string
	Final bool
	Err   error
}

// Session is a recording being recognized.
type Session struct{ r *recording }

// Stop ends capture and lets the cloud finish the text; emit then gets the
// final Result.
func (s *Session) Stop() { s.r.Stop() }

// Cancel discards the recording at once.
func (s *Session) Cancel() { s.r.Cancel() }

// Start records the default input and recognizes it through WeChat's cloud
// service; dir keeps the service's device identity. emit runs on other
// goroutines, the last time with a Final Result. Start may wait for the
// user to answer the microphone prompt, so call it off the UI.
func Start(ctx context.Context, dir string, emit func(Result)) (*Session, error) {
	// MaxDuration ends capture; this bounds the cloud round trips that follow.
	ctx, cancel := context.WithTimeout(ctx, MaxDuration+30*time.Second)
	r := &recording{audio: make(chan []byte, 640), cancel: cancel}
	stream, err := microphone.Capture(SampleRate, r.samples)
	if err != nil {
		cancel()
		return nil, err
	}
	r.mu.Lock()
	r.stream = stream
	r.mu.Unlock()
	// The system may take the microphone away, as iOS does for a call: what
	// was said so far is still recognized.
	go func() {
		select {
		case <-stream.Ended():
			r.end(nil)
		case <-ctx.Done():
		}
	}()
	limit := time.AfterFunc(MaxDuration, r.Stop)
	go func() {
		defer cancel()
		defer limit.Stop()
		defer stream.Stop()
		text, err := Recognize(ctx, dir, r.audio, func(text string) { emit(Result{Text: text}) })
		if captureErr := r.captureErr(); captureErr != nil {
			err = captureErr
		}
		emit(Result{Text: text, Final: true, Err: err})
	}()
	return &Session{r: r}, nil
}

// Recognize streams SampleRate PCM from audio to WeChat's cloud recognizer
// until audio closes, reporting partial text, and returns the final text.
func Recognize(ctx context.Context, dir string, audio <-chan []byte, partial func(string)) (string, error) {
	return recognizeWechat(ctx, dir, audio, partial)
}

// recording hands microphone audio to the recognizer. Stop ends capture and
// waits for the cloud's final text; Cancel discards the recording at once.
type recording struct {
	mu           sync.Mutex
	audio        chan []byte
	captureError error
	closed       bool
	stream       *microphone.Stream
	cancel       context.CancelFunc
	stopOnce     sync.Once
}

func (r *recording) Stop() {
	r.stopOnce.Do(func() {
		r.stopStream()
		r.end(nil)
		// Also bound finalization when release precedes the cloud handshake.
		time.AfterFunc(20*time.Second, r.cancel)
	})
}

func (r *recording) Cancel() {
	r.cancel()
	r.stopStream()
	r.end(nil)
}

func (r *recording) stopStream() {
	r.mu.Lock()
	s := r.stream
	r.mu.Unlock()
	if s != nil {
		s.Stop()
	}
}

func (r *recording) captureErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.captureError
}

// samples runs on the system's audio thread; it never blocks.
func (r *recording) samples(s []int16) {
	pcm := make([]byte, 2*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v))
	}
	r.push(pcm)
}

// push queues audio for the recognizer, ending capture when the bounded
// buffer is full rather than blocking the audio thread.
func (r *recording) push(pcm []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || len(pcm) == 0 {
		return
	}
	select {
	case r.audio <- pcm:
	default:
		r.closeLocked(errors.New("voice audio buffer full"))
	}
}

// end closes the audio once, recording why when err is not nil.
func (r *recording) end(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closeLocked(err)
	}
}

func (r *recording) closeLocked(err error) {
	r.captureError = err
	r.closed = true
	close(r.audio)
}
