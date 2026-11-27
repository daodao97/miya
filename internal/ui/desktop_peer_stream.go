package desktopui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image/jpeg"
	"net/http"
	"time"

	"miya/internal/uiruntime"
)

func (p *peerDesktop) stopStreaming() {
	if p.streamCancel != nil {
		p.streamCancel()
	}
	p.stream, p.streamCtx, p.streamCancel = nil, nil, nil
}

func (p *peerDesktop) startStreaming() {
	leaseCtx, client := p.ctx, p.client
	ctx, cancel := context.WithCancel(leaseCtx)
	p.streamCtx, p.streamCancel = ctx, cancel
	p.image = nil
	apply := func(f func()) {
		p.apply(leaseCtx, func() {
			if p.streamCtx == ctx {
				f()
			}
		})
	}
	go func() {
		defer apply(func() {
			p.stopStreaming()
			p.discardInput()
			p.image = nil
			p.session.FrameStream = false
		})
		dialCtx, stop := context.WithTimeout(ctx, 8*time.Second)
		stream, err := client.OpenFrameStream(dialCtx)
		stop()
		if err != nil {
			return
		}
		defer stream.Close()
		apply(func() { p.stream = stream })
		var previousHash [32]byte
		var haveFrame bool
		for {
			readCtx, done := context.WithTimeout(ctx, 10*time.Second)
			data, notice, err := stream.Receive(readCtx)
			done()
			if err != nil {
				return
			}
			if notice != nil {
				if !notice.Input {
					haveFrame = false
				}
				apply(func() {
					if notice.Input {
						p.showError(&notice.HTTPError)
					} else if notice.Status == http.StatusLocked {
						p.receiveFrame(nil, [32]byte{}, true, nil)
					} else {
						p.receiveFrame(nil, [32]byte{}, false, &notice.HTTPError)
					}
				})
				continue
			}
			hash := sha256.Sum256(data)
			if haveFrame && hash == previousHash {
				continue
			}
			frame, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				return
			}
			previousHash, haveFrame = hash, true
			applied := make(chan struct{})
			apply(func() {
				defer close(applied)
				bitmap := uiruntime.UpdateStreamBitmap(p.image, frame)
				p.receiveFrame(bitmap, hash, false, nil)
			})
			select {
			case <-ctx.Done():
				return
			case <-applied:
			}
		}
	}()
}
