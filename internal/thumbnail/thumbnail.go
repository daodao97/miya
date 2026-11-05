// Package thumbnail makes the bounded JPEG previews a phone shows for images.
package thumbnail

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"miya/internal/remote"

	xdraw "golang.org/x/image/draw"
)

const Edge = 640
const MaxBytes = remote.MaxPreviewBytes

type Image struct {
	Source string `json:"source"`
	Data   []byte `json:"data,omitempty"`
}

// File is called only for image sources in the subscribed history.
// Thumbnail bytes travel separately from messages, keeping history deltas
// small and their desktop source identities intact.
func File(source string) ([]byte, error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "file" || u.Host != "" && u.Host != "localhost" || !filepath.IsAbs(u.Path) {
		return nil, errors.New("Invalid image source")
	}
	f, err := os.Open(u.Path)
	if err != nil {
		return nil, errors.New("Original image unavailable")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, remote.MaxImageBytes+1))
	if err != nil || len(data) > remote.MaxImageBytes {
		return nil, errors.New("Unable to read image preview")
	}
	return Data(data)
}

func Data(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 64_000_000 {
		return nil, errors.New("Unable to decode image preview")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("Unable to decode image preview")
	}
	w, h := config.Width, config.Height
	if edge := max(w, h); edge > Edge {
		w, h = max(1, w*Edge/edge), max(1, h*Edge/edge)
	}
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(thumb, thumb.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), img, img.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, thumb, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	if out.Len() > MaxBytes {
		return nil, errors.New("Preview image is too large")
	}
	return out.Bytes(), nil
}
