package main

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

const phonePreviewEdge = 640
const maxPhonePreviewBytes = remote.MaxPreviewBytes

type phoneImagePreview struct {
	Source string `json:"source"`
	Data   []byte `json:"data,omitempty"`
}

// phonePreview is called only for image sources in the subscribed history.
// Thumbnail bytes travel separately from messages, keeping history deltas
// small and their desktop source identities intact.
func phonePreview(source string) ([]byte, error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "file" || u.Host != "" && u.Host != "localhost" || !filepath.IsAbs(u.Path) {
		return nil, errors.New("无效的图片来源")
	}
	f, err := os.Open(u.Path)
	if err != nil {
		return nil, errors.New("原图片已不可用")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPhoneImageBytes+1))
	if err != nil || len(data) > maxPhoneImageBytes {
		return nil, errors.New("无法读取图片预览")
	}
	return phonePreviewData(data)
}

func phonePreviewData(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 64_000_000 {
		return nil, errors.New("无法解码图片预览")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("无法解码图片预览")
	}
	w, h := config.Width, config.Height
	if edge := max(w, h); edge > phonePreviewEdge {
		w, h = max(1, w*phonePreviewEdge/edge), max(1, h*phonePreviewEdge/edge)
	}
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(thumb, thumb.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), img, img.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, thumb, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	if out.Len() > maxPhonePreviewBytes {
		return nil, errors.New("预览图片过大")
	}
	return out.Bytes(), nil
}
