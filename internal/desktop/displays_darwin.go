//go:build darwin && !ios

package desktop

import dc "github.com/egoist/mygo/plugins/desktopcontrol"

func Displays() ([]Display, error) {
	ds, err := dc.Displays()
	if err != nil {
		return nil, desktopError(err)
	}
	out := []Display{}
	for _, d := range ds {
		out = append(out, Display{ID: d.ID, UUID: d.UUID, Name: d.Name, Main: d.Main, Width: d.PixelWidth, Height: d.PixelHeight})
	}
	return out, nil
}

func displayInfo(d Display) (dc.Display, error) {
	ds, err := dc.Displays()
	if err != nil {
		return dc.Display{}, desktopError(err)
	}
	for _, v := range ds {
		if d.ID == 0 && v.Main || d.ID == v.ID && d.UUID == v.UUID && d.UUID != "" {
			return v, nil
		}
	}
	if d.ID == 0 {
		return dc.Display{}, permissionError(13)
	}
	return dc.Display{}, ErrDisplayUnavailable
}

func CaptureDisplay(d Display) ([]byte, error) {
	if d.ID == 0 || d.UUID == "" {
		return nil, ErrDisplayUnavailable
	}
	return captureTarget(dc.Display{ID: d.ID, UUID: d.UUID}, dc.Window{})
}

func SendDisplayInput(d Display, in Input) error {
	if d.ID == 0 || d.UUID == "" {
		return ErrDisplayUnavailable
	}
	return sendInput(Window{}, d, in)
}
