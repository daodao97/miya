package control

import (
	"net/url"
	"strconv"

	"miya/internal/desktop"
)

const (
	MinFrameEdge = 320
	MaxFrameEdge = 4096
)

func frameOptions(query url.Values) (desktop.FrameOptions, bool) {
	if !query.Has("maxEdge") && !query.Has("quality") {
		return desktop.FrameOptions{}, true
	}
	edge, edgeErr := strconv.Atoi(query.Get("maxEdge"))
	quality, qualityErr := strconv.Atoi(query.Get("quality"))
	if edgeErr != nil || qualityErr != nil || edge < MinFrameEdge || edge > MaxFrameEdge || quality < 50 || quality > 90 {
		return desktop.FrameOptions{}, false
	}
	return desktop.FrameOptions{MaxEdge: edge, Quality: quality}, true
}
