package client

import (
	"context"
	"crypto/sha256"
	"net/http"
	"time"

	"miya/internal/phone/desktopview"
)

func (p *MobileDesktopPage) stopStreaming() {
	if p.streamCancel != nil {
		p.streamCancel()
	}
	p.streamCtx, p.streamCancel = nil, nil
}

func (m *Client) startDesktopStream(p *MobileDesktopPage) {
	ctx, cancel := context.WithCancel(p.Ctx)
	p.streamCtx, p.streamCancel = ctx, cancel
	client, gen := p.Client, p.gen
	post := func(f func()) {
		m.onMain(gen, func() {
			if p.streamCtx == ctx && ctx.Err() == nil && m.DesktopPage == p && p.Active {
				f()
			}
		})
	}
	go func() {
		defer post(func() {
			p.stopStreaming()
			p.frameStream = false
			m.DesktopPoll(p)
		})
		dial, release := context.WithTimeout(ctx, 8*time.Second)
		stream, err := client.OpenFrameStream(dial)
		release()
		if err != nil {
			return
		}
		defer stream.Close()
		var previous [32]byte
		haveFrame := false
		for ctx.Err() == nil {
			read, stop := context.WithTimeout(ctx, 10*time.Second)
			data, notice, err := stream.Receive(read)
			stop()
			if err != nil {
				return
			}
			var frame desktopview.ImageSource
			if notice == nil {
				hash := sha256.Sum256(data)
				if haveFrame && previous == hash {
					continue
				}
				image, err := desktopview.DecodeImage(data)
				if err != nil {
					return
				}
				frame, previous, haveFrame = image, hash, true
			} else if !notice.Input {
				haveFrame = false
			}
			applied := make(chan struct{})
			post(func() {
				defer close(applied)
				switch {
				case notice == nil:
					m.ReceiveDesktopFrame(p, data, frame, false, nil)
				case !notice.Input:
					m.ReceiveDesktopFrame(p, nil, nil, notice.Status == http.StatusLocked, &notice.HTTPError)
				case notice.Status == http.StatusGone:
					m.DesktopFinish(p, notice.Message)
				case notice.Status != http.StatusTooManyRequests:
					p.Status = notice.Message
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
