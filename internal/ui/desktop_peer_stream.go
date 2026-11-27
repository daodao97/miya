package desktopui

import (
	"context"
	"crypto/sha256"
	"net/http"
	"time"

	"github.com/egoist/mygo/ui"
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
		var image ui.ImageSource
		var previousHash [32]byte
		for {
			readCtx, done := context.WithTimeout(ctx, 10*time.Second)
			data, notice, err := stream.Receive(readCtx)
			done()
			if err != nil {
				return
			}
			if notice != nil {
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
			if image == nil || hash != previousHash {
				image, err = ui.DecodeImage(data)
				if err != nil || image == nil {
					return
				}
				previousHash = hash
			}
			frame := image
			apply(func() { p.receiveFrame(frame, hash, false, nil) })
		}
	}()
}
