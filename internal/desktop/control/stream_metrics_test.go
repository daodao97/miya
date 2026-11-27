package control

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
)

// This probe measures the local transport and JPEG decode with pre-encoded
// frames. It does not require screen recording or send OS input events.
func TestDesktopStreamTransportMetrics(t *testing.T) {
	if os.Getenv("MIYA_DESKTOP_STREAM_METRICS") != "1" {
		t.Skip("opt-in transport timing")
	}
	scene := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for y := range 720 {
		for x := range 1280 {
			scene.SetRGBA(x, y, color.RGBA{
				R: uint8(x ^ y), G: uint8(x + 3*y), B: uint8(x/3 ^ y/2), A: 255,
			})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, scene, &jpeg.Options{Quality: 65}); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture: 1280x720 JPEG, %d bytes", encoded.Len())
	const samples = 30
	for _, name := range []string{"legacy-mobile-poll", "stream"} {
		t.Run(name, func(t *testing.T) {
			d, token, _ := mockDesktop(t)
			d.AllowPick(token)
			var lastInput atomic.Int32
			capture := func() ([]byte, error) {
				return append(bytes.Clone(encoded.Bytes()), byte(lastInput.Load())), nil
			}
			d.Capture = capture
			d.CaptureFrame = func(context.Context, *desktop.Display, *desktop.Window, desktop.FrameOptions) ([]byte, error) {
				return capture()
			}
			d.Input = func(in desktop.Input) error {
				lastInput.Store(int32(in.DeltaY))
				return nil
			}
			client := testDesktopClient(t, d, token)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := client.Start(ctx); err != nil {
				t.Fatal(err)
			}
			receive := func() ([]byte, error) {
				data, _, err := client.Frame(ctx)
				return data, err
			}
			send := client.InputForDisplay
			if name == "stream" {
				stream, err := client.OpenFrameStream(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				receive = func() ([]byte, error) {
					data, notice, err := stream.Receive(ctx)
					if notice != nil {
						return nil, &notice.HTTPError
					}
					return data, err
				}
				send = stream.SendInput
			}
			read := func() byte {
				t.Helper()
				data, err := receive()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
				return data[len(data)-1]
			}
			read()
			began := time.Now()
			var latencies []time.Duration
			for seq := 1; seq <= samples; seq++ {
				started := time.Now()
				if err := send(ctx, desktop.Input{Kind: "scroll", X: .5, Y: .5, DeltaY: seq}, desktop.Display{}); err != nil {
					t.Fatal(err)
				}
				if name == "legacy-mobile-poll" && !Wait(ctx, time.Until(started.Add(300*time.Millisecond))) {
					t.Fatal(ctx.Err())
				}
				for read() != byte(seq) {
				}
				latencies = append(latencies, time.Since(started))
			}
			fps := float64(samples) / time.Since(began).Seconds()
			slices.Sort(latencies)
			t.Logf("%.2f visible updates/s, input-to-decode p50 %.1f ms, p95 %.1f ms",
				fps, float64(latencies[samples/2])/float64(time.Millisecond),
				float64(latencies[(95*samples+99)/100-1])/float64(time.Millisecond))
		})
	}
}
