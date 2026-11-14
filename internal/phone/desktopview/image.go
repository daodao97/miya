// Package desktopview contains phone remote-desktop image and input geometry.
// It deliberately imports neither phone UI framework.
package desktopview

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

type Frame struct {
	Data          []byte
	Width, Height float32
	Format        string
}
type ImageSource = *Frame

func DecodeRaster(data []byte) (image.Image, error) {
	if len(data) == 0 || len(data) > 2<<20 {
		return nil, errors.New("invalid or oversized frame")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16384 || cfg.Height > 16384 || int64(cfg.Width)*int64(cfg.Height) > 64<<20 {
		return nil, errors.New("invalid frame dimensions")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}
func DecodeImage(data []byte) (*Frame, error) {
	if len(data) == 0 || len(data) > 2<<20 {
		return nil, errors.New("invalid or oversized frame")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16384 || cfg.Height > 16384 || int64(cfg.Width)*int64(cfg.Height) > 64<<20 {
		return nil, errors.New("invalid frame dimensions")
	}
	// Validate before publishing; the native image node consumes the original
	// encoded data. No decoded raster or temporary file survives this worker.
	if _, err = DecodeRaster(data); err != nil {
		return nil, err
	}
	return &Frame{Data: data, Width: float32(cfg.Width), Height: float32(cfg.Height), Format: format}, nil
}
func ImageSize(f *Frame) (float32, float32) {
	if f == nil {
		return 0, 0
	}
	return f.Width, f.Height
}

type Rect struct{ X, Y, W, H float32 }

func (r Rect) Contains(x, y float32) bool { return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H }

type Point struct{ X, Y float64 }
type InputKind uint8

const (
	InputPointerDown InputKind = iota + 1
	InputPointerMove
	InputPointerUp
	InputScroll
	InputZoom
)

type InputEvent struct {
	Kind                InputKind
	X, Y, DX, DY, Scale float32
	Button              int
	Canceled            bool
}
