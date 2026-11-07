package voiceinput

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Result struct {
	Text  string
	Final bool
	Err   error
}

type Recording interface {
	Stop()
	Cancel()
}

// Session owns both capture and transcription. Stop ends capture and waits
// for the cloud's final text; Cancel discards the recording immediately.
type Session struct {
	mu           sync.Mutex
	audio        chan []byte
	results      chan Result
	captureError error
	closed       bool
	recording    Recording
	cancel       context.CancelFunc
	stopOnce     sync.Once
}

func (s *Session) Results() <-chan Result { return s.results }
func (s *Session) Stop() {
	s.stopOnce.Do(func() {
		s.recording.Stop()
		// Also bound finalization when release precedes the cloud handshake.
		time.AfterFunc(20*time.Second, s.cancel)
	})
}
func (s *Session) Cancel() { s.cancel(); s.recording.Cancel() }

func Start(ctx context.Context, dir string) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	s := &Session{audio: make(chan []byte, 640), results: make(chan Result, 1), cancel: cancel}
	r, err := startAudio(ctx, s.capture)
	if err != nil {
		cancel()
		return nil, err
	}
	s.recording = r
	s.mu.Lock()
	captureError := s.captureError
	s.mu.Unlock()
	if captureError != nil {
		r.Cancel()
		cancel()
		return nil, captureError
	}
	go func() {
		defer cancel()
		defer r.Cancel()
		defer close(s.results)
		text, err := recognizeWechat(ctx, dir, s.audio, func(text string) { s.emit(Result{Text: text}) })
		s.mu.Lock()
		captureError := s.captureError
		s.mu.Unlock()
		if captureError != nil {
			err = captureError
		}
		s.emit(Result{Text: text, Final: true, Err: err})
	}()
	go func() {
		timer := time.NewTimer(60 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			r.Cancel()
		case <-timer.C:
			s.Stop()
		}
	}()
	return s, nil
}

func (s *Session) emit(r Result) {
	select {
	case <-s.results:
	default:
	}
	s.results <- r
}

func (s *Session) capture(b []byte, err error, end bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if len(b) > 0 {
		select {
		case s.audio <- b:
		default:
			err = errors.New("voice audio buffer full")
			end = true
		}
	}
	if err != nil || end {
		s.captureError = err
		s.closed = true
		close(s.audio)
	}
}
