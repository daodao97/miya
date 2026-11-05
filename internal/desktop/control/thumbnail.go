package control

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"strconv"

	"golang.org/x/image/draw"
	"miya/internal/desktop"
)

const desktopThumbnailSize = 480

// serveThumbnail previews one listed window for the phone's window picker.
// Only menu leases may preview: an authorization lease must not show other
// windows. The caller holds d.work, so previews never overlap captures.
func (d *Server) serveThumbnail(w http.ResponseWriter, r *http.Request, l *desktopLease, pick bool) {
	if !pick {
		http.Error(w, "Window previews are not available in this view", 403)
		return
	}
	id, err1 := strconv.ParseUint(r.URL.Query().Get("id"), 10, 32)
	pid, err2 := strconv.Atoi(r.URL.Query().Get("pid"))
	if err1 != nil || err2 != nil || id == 0 {
		http.Error(w, "Invalid window", 400)
		return
	}
	windows, err := d.Windows()
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	for _, candidate := range windows {
		if candidate.ID != uint32(id) || candidate.PID != pid {
			continue
		}
		d.keepAwake(l, false)
		data, err := d.CaptureWindow(candidate)
		if errors.Is(err, desktop.ErrWindowUnavailable) {
			http.Error(w, "The window closed. Refresh the list.", 404)
			return
		}
		if err == nil {
			data, err = desktopThumbnail(data, desktopThumbnailSize)
		}
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(data)
		return
	}
	http.Error(w, "The window closed. Refresh the list.", 404)
}

// desktopThumbnail shrinks a captured JPEG to at most size pixels per side.
func desktopThumbnail(data []byte, size int) ([]byte, error) {
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("Unable to generate a window preview")
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= size && h <= size {
		return data, nil
	}
	if w >= h {
		w, h = size, max(1, h*size/w)
	} else {
		w, h = max(1, w*size/h), size
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 70}); err != nil {
		return nil, errors.New("Unable to generate a window preview")
	}
	return out.Bytes(), nil
}
