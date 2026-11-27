package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"miya/internal/desktop"
)

// Frames have a bounded receive window. Slow links cannot accumulate seconds
// of screenshots ahead of the viewer, and input travels in the other direction.
const streamFrameCredits = 4
const streamTimeout = 5 * time.Second

type StreamNotice struct {
	HTTPError
	Input bool `json:"input,omitempty"`
}

type FrameStream struct{ conn *websocket.Conn }

func (c *Client) OpenFrameStream(ctx context.Context) (*FrameStream, error) {
	conn, resp, err := websocket.Dial(ctx, "ws://desktop"+Prefix+"/stream", &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}},
	})
	if err != nil {
		if resp != nil {
			return nil, &HTTPError{Status: resp.StatusCode, Message: tr("Unable to connect to your Mac. Check that it is online and retry.")}
		}
		return nil, err
	}
	conn.SetReadLimit(desktopClientMaxBody)
	return &FrameStream{conn}, nil
}

func (s *FrameStream) Close() { _ = s.conn.CloseNow() }

func (s *FrameStream) Receive(ctx context.Context) ([]byte, *StreamNotice, error) {
	kind, data, err := s.conn.Read(ctx)
	if err != nil {
		return nil, nil, err
	}
	if kind == websocket.MessageBinary {
		if err := s.conn.Write(ctx, websocket.MessageText, []byte("ack")); err != nil {
			return nil, nil, err
		}
		return data, nil, nil
	}
	var notice StreamNotice
	if err := json.Unmarshal(data, &notice); err != nil {
		return nil, nil, err
	}
	return nil, &notice, nil
}

func (s *FrameStream) SendInput(ctx context.Context, in desktop.Input, display desktop.Display) error {
	if err := in.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(desktopInputRequest{Input: in, DisplayID: display.ID, DisplayUUID: display.UUID})
	if err != nil {
		return err
	}
	return s.conn.Write(ctx, websocket.MessageText, data)
}

// Called under Server.mu whenever a target changes.
func (l *desktopLease) cancelStream() {
	if l.streamCancel != nil {
		l.streamCancel()
		l.streamCancel = nil
	}
}

func (d *Server) serveStream(w http.ResponseWriter, r *http.Request, l *desktopLease) {
	d.mu.Lock()
	if d.valid(l.token) != l || !l.active || !l.pick || d.CaptureFrame == nil {
		d.mu.Unlock()
		http.Error(w, "Desktop stream unavailable", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(l.ctx, cancel)()
	l.cancelStream()
	l.streamCancel = cancel
	window, display := l.window, l.display
	d.mu.Unlock()

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(32 << 10)
	credits := make(chan struct{}, streamFrameCredits)
	for range streamFrameCredits {
		credits <- struct{}{}
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer cancel()
		d.readStreamInput(ctx, conn, l, display, window, credits)
	}()
	defer func() { cancel(); <-readerDone }()

	options := desktop.FrameOptions{MaxEdge: InteractiveFrameMaxEdge, Quality: 65}
	creditTimer := time.NewTimer(streamTimeout)
	creditTimer.Stop()
	defer creditTimer.Stop()
	for ctx.Err() == nil {
		creditTimer.Reset(streamTimeout)
		select {
		case <-ctx.Done():
			return
		case <-creditTimer.C:
			return
		case <-credits:
			creditTimer.Stop()
		}
		started := time.Now()
		interval := InteractiveFrameInterval
		if !d.work.TryLock() {
			returnStreamCredit(credits)
		} else {
			data, locked, failure := d.captureStreamFrame(ctx, l, display, window, options)
			d.work.Unlock()
			if ctx.Err() != nil {
				return
			}
			if locked || failure != nil {
				returnStreamCredit(credits)
				interval = 750 * time.Millisecond
				if locked {
					failure = &HTTPError{Status: http.StatusLocked}
				}
				if !writeStreamNotice(ctx, conn, *failure, false) || failure.Status == 409 || failure.Status == 410 {
					return
				}
			} else {
				writeCtx, stop := context.WithTimeout(ctx, streamTimeout)
				err := conn.Write(writeCtx, websocket.MessageBinary, data)
				stop()
				if err != nil {
					return
				}
			}
		}
		if remaining := time.Until(started.Add(interval)); remaining > 0 && !Wait(ctx, remaining) {
			return
		}
	}
}

func (d *Server) captureStreamFrame(ctx context.Context, l *desktopLease, display *desktop.Display, window *desktop.Window, options desktop.FrameOptions) ([]byte, bool, *HTTPError) {
	d.mu.Lock()
	if d.valid(l.token) != l || !l.active || ctx.Err() != nil {
		d.mu.Unlock()
		return nil, false, &HTTPError{Status: 410, Message: "Remote control ended"}
	}
	l.expires = d.leaseTime().Add(desktopControlIdleTimeout)
	l.lastFrame = time.Now()
	d.mu.Unlock()
	captureCtx, stop := context.WithTimeout(ctx, streamTimeout)
	defer stop()
	return d.captureFrame(captureCtx, l, display, window, options, true)
}

func (d *Server) readStreamInput(ctx context.Context, conn *websocket.Conn, l *desktopLease, display *desktop.Display, window *desktop.Window, credits chan struct{}) {
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil || kind != websocket.MessageText {
			return
		}
		if bytes.Equal(data, []byte("ack")) {
			returnStreamCredit(credits)
			continue
		}
		var request desktopInputRequest
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
			return
		}
		failure := d.performInput(ctx, l, display, window, request)
		if failure != nil && failure.Status == 429 && Wait(ctx, 20*time.Millisecond) {
			failure = d.performInput(ctx, l, display, window, request)
		}
		if failure != nil && (!writeStreamNotice(ctx, conn, *failure, true) || failure.Status == 410) {
			return
		}
	}
}

func returnStreamCredit(credits chan struct{}) {
	select {
	case credits <- struct{}{}:
	default:
	}
}

func writeStreamNotice(ctx context.Context, conn *websocket.Conn, failure HTTPError, input bool) bool {
	data, _ := json.Marshal(StreamNotice{HTTPError: failure, Input: input})
	writeCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data) == nil
}
