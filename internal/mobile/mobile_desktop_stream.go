package mobile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	"image/jpeg"
	"net/http"
	"time"

	"miya/internal/uiruntime"
)

func (p *MobileDesktopPage) stopStreaming() {
	if p.streamCancel != nil {
		p.streamCancel()
	}
	p.stream, p.streamCtx, p.streamCancel = nil, nil, nil
}

func (m *App) startDesktopStream(p *MobileDesktopPage) {
	ctx, cancel := context.WithCancel(p.Ctx)
	p.streamCtx, p.streamCancel = ctx, cancel
	client, gen := p.Client, p.gen
	apply := func(f func()) {
		if ctx.Err() != nil {
			return
		}
		m.onMain(gen, func() {
			if p.streamCtx == ctx && ctx.Err() == nil && m.DesktopPage == p && p.Active {
				f()
			}
		})
	}
	go func() {
		defer apply(func() {
			p.stopStreaming()
			p.Queue, p.Image = nil, nil
			p.frameStream = false
			m.DesktopPoll(p)
		})
		dialCtx, stop := context.WithTimeout(ctx, 8*time.Second)
		stream, err := client.OpenFrameStream(dialCtx)
		stop()
		if err != nil {
			return
		}
		defer stream.Close()
		var previousHash [32]byte
		var haveFrame bool
		for {
			readCtx, done := context.WithTimeout(ctx, 10*time.Second)
			data, notice, err := stream.Receive(readCtx)
			done()
			if err != nil {
				return
			}
			var decoded image.Image
			var hash [32]byte
			if notice == nil {
				hash = sha256.Sum256(data)
				if haveFrame && hash == previousHash {
					continue
				}
				decoded, err = jpeg.Decode(bytes.NewReader(data))
				if err != nil {
					return
				}
				previousHash, haveFrame = hash, true
			} else if !notice.Input {
				haveFrame = false
			}
			// Backpressure keeps a busy UI from accumulating decoded frames.
			applied := make(chan struct{})
			apply(func() {
				defer close(applied)
				switch {
				case notice == nil:
					p.stream = stream
					bitmap := uiruntime.UpdateStreamBitmap(p.Image, decoded)
					m.applyDesktopFrame(p, hash, bitmap, false, nil)
				case notice.Input:
					if notice.Status == http.StatusGone {
						m.DesktopFinish(p, notice.Message)
					} else if notice.Status != http.StatusTooManyRequests {
						p.Status = notice.Message
					}
				default:
					m.applyDesktopFrame(p, hash, nil, notice.Status == http.StatusLocked, &notice.HTTPError)
				}
			})
			select {
			case <-ctx.Done():
				return
			case <-applied:
			}
		}
	}()
}
