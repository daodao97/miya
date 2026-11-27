package mobile

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/desktop/control/controltest"
	"miya/internal/uiruntime"
)

func TestDesktopPageUsesStreamForFullScreenAndInput(t *testing.T) {
	var frames [2]bytes.Buffer
	scene := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for index := range frames {
		scene.Set(0, 0, color.RGBA{R: uint8(index * 255), A: 255})
		if err := jpeg.Encode(&frames[index], scene, nil); err != nil {
			t.Fatal(err)
		}
	}
	var scrolled atomic.Bool
	inputs := make(chan desktop.Input, 1)
	// Only the streaming capture path is available in this fixture.
	d := &control.Server{Native: control.Native{
		Authorize: func() error { return nil },
		CaptureFrame: func(context.Context, *desktop.Display, *desktop.Window, desktop.FrameOptions) ([]byte, error) {
			if scrolled.Load() {
				return frames[1].Bytes(), nil
			}
			return frames[0].Bytes(), nil
		},
		Input: func(in desktop.Input) error {
			scrolled.Store(true)
			inputs <- in
			return nil
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	token, err := d.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.AllowPick(token)
	client := controltest.Client(t, d, token)

	posts := make(chan func(), 16)
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { posts <- f }
	defer func() {
		cancel()
		uiruntime.RunOnMain = old
	}()
	m := NewMobileApp()
	p := &MobileDesktopPage{Client: client, Ctx: ctx, Cancel: cancel}
	m.DesktopPage = p
	m.DesktopInitialize(p)
	for p.Image == nil {
		select {
		case f := <-posts:
			f()
		case <-ctx.Done():
			t.Fatalf("no streamed frame: phase=%s, detail=%s", p.Phase, p.Detail)
		}
	}
	if p.stream == nil || p.Polling {
		t.Fatal("full-screen page did not switch to streaming")
	}
	firstImage, firstHash := p.Image, p.ImageHash
	m.DesktopSend(p, desktop.Input{Kind: "scroll", X: .5, Y: .5, DeltaY: 24})
	select {
	case in := <-inputs:
		if in.Kind != "scroll" || in.DeltaY != 24 {
			t.Fatalf("unexpected streamed input: %+v", in)
		}
	case <-ctx.Done():
		t.Fatal("streamed input did not arrive")
	}
	for p.Sending || p.ImageHash == firstHash {
		select {
		case f := <-posts:
			f()
		case <-ctx.Done():
			t.Fatal("input dispatch or frame update did not complete")
		}
	}
	if p.Image != firstImage {
		t.Fatal("same-size update replaced the stream bitmap")
	}
	streamCtx := p.streamCtx
	p.InvalidateTarget()
	if streamCtx.Err() == nil || p.stream != nil || p.Active || p.Image != nil {
		t.Fatal("target invalidation did not close and clear the mobile stream")
	}
}

func TestDesktopPageFallsBackWhenStreamIsUnavailable(t *testing.T) {
	var frame bytes.Buffer
	if err := jpeg.Encode(&frame, image.NewRGBA(image.Rect(0, 0, 32, 16)), nil); err != nil {
		t.Fatal(err)
	}
	d := &control.Server{Native: control.Native{
		Capture:   func() ([]byte, error) { return frame.Bytes(), nil },
		Authorize: func() error { return nil },
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	token, err := d.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client := controltest.Client(t, d, token)
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	posts := make(chan func(), 16)
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { posts <- f }
	defer func() {
		cancel()
		uiruntime.RunOnMain = old
	}()
	m := NewMobileApp()
	p := &MobileDesktopPage{Client: client, Ctx: ctx, Cancel: cancel, Active: true, frameStream: true}
	m.DesktopPage = p
	m.DesktopPoll(p)
	for p.Image == nil {
		select {
		case f := <-posts:
			f()
		case <-ctx.Done():
			t.Fatal("polling did not resume after stream failure")
		}
	}
	if p.streamCtx != nil || p.frameStream {
		t.Fatal("failed stream was left active")
	}
}
