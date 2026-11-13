package desktopcontrol

import (
	"context"
	"errors"
	"math"
)

var (
	ErrShareableContent = errors.New("desktopcontrol: unable to list shareable content")
	ErrCapture          = errors.New("desktopcontrol: screen capture failed")
	ErrEncoding         = errors.New("desktopcontrol: image encoding failed")
)

// CaptureOptions controls a JPEG screenshot. MaxDimension bounds its longest
// edge in pixels (zero uses the target's size); Quality is between 0 and 1.
type CaptureOptions struct {
	MaxDimension int
	Quality      float64
}

func (o CaptureOptions) validate() error {
	if o.MaxDimension < 0 || o.MaxDimension > 16384 || math.IsNaN(o.Quality) || o.Quality < 0 || o.Quality > 1 {
		return errors.New("desktopcontrol: invalid capture options")
	}
	return nil
}

// CaptureDisplay captures the active display with the same ID and UUID.
// Cancellation returns immediately; late native results are discarded.
// Call off the application's main thread, which ScreenCaptureKit may need.
func CaptureDisplay(ctx context.Context, d Display, o CaptureOptions) ([]byte, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if d.ID == 0 || d.UUID == "" {
		return nil, ErrDisplayUnavailable
	}
	return capture(ctx, d, Window{}, o)
}

// CaptureWindow captures just w, never falling back to its display.
func CaptureWindow(ctx context.Context, w Window, o CaptureOptions) ([]byte, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if w.ID == 0 || w.PID <= 0 {
		return nil, ErrWindowUnavailable
	}
	return capture(ctx, Display{}, w, o)
}
