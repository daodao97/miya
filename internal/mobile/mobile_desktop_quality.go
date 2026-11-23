package mobile

import (
	"math"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
)

type desktopFrameQuality struct {
	reduction  int
	fast, slow int
}

var desktopFrameLevels = [...]struct {
	scale   float64
	quality int
}{
	{1, 90},
	{.85, 82},
	{.7, 70},
	{.5, 55},
}

func (p *MobileDesktopPage) frameOptions() desktop.FrameOptions {
	view, zoom := p.View, max(1, p.Zoom)
	if p.Floating {
		view, zoom = p.float.view, 1
	}
	width, height := desktopFitSize(p.Image, view)
	if width <= 0 || height <= 0 || p.frameScale <= 0 {
		return desktop.FrameOptions{}
	}
	level := desktopFrameLevels[p.frameQuality.reduction]
	pixels := float64(max(width, height)*zoom*p.frameScale) * level.scale
	edge := max(control.MinFrameEdge, min(control.MaxFrameEdge, int(math.Ceil(pixels))))
	return desktop.FrameOptions{MaxEdge: edge, Quality: level.quality}
}

func (q *desktopFrameQuality) observe(elapsed time.Duration, err error) {
	status := control.Status(err)
	switch {
	case (status == 0 || status == 503) && elapsed >= 650*time.Millisecond:
		q.fast = 0
		q.slow++
		if q.slow >= 2 {
			q.reduction = min(q.reduction+1, len(desktopFrameLevels)-1)
			q.slow = 0
		}
	case err == nil && elapsed < 350*time.Millisecond:
		q.slow = 0
		q.fast++
		// Probe upward more slowly to avoid oscillating on a marginal link.
		if q.fast >= 8 {
			q.reduction = max(q.reduction-1, 0)
			q.fast = 0
		}
	case status != 429 && status != 423:
		q.fast, q.slow = 0, 0
	}
}
