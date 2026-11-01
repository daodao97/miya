package main

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/png"
	"sync"

	"golang.org/x/image/vector"
)

//go:embed assets/tray/*.png
var usageTrayAssets embed.FS

var miyaTrayIcon = sync.OnceValue(func() []byte {
	canvas := image.NewNRGBA(image.Rect(0, 0, 36, 36))
	p := vector.NewRasterizer(36, 36)
	p.MoveTo(6, 29)
	p.LineTo(6, 7)
	p.LineTo(11, 7)
	p.LineTo(18, 19)
	p.LineTo(25, 7)
	p.LineTo(30, 7)
	p.LineTo(30, 29)
	p.LineTo(26, 29)
	p.LineTo(26, 14)
	p.LineTo(18, 27)
	p.LineTo(10, 14)
	p.LineTo(10, 29)
	p.ClosePath()
	p.Draw(canvas, canvas.Bounds(), image.NewUniform(color.NRGBA{A: 255}), image.Point{})
	var out bytes.Buffer
	_ = png.Encode(&out, canvas)
	return out.Bytes()
})

func usageTrayIconFor(agent string) []byte {
	if agent == "codex" || agent == "claude" {
		if data, err := usageTrayAssets.ReadFile("assets/tray/" + agent + ".png"); err == nil {
			return data
		}
	}
	return miyaTrayIcon()
}
