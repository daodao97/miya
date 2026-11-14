package nswidgets

import (
	"encoding/base64"
	"sync"

	"github.com/nsgo-dev/nsgo/core"

	catalog "miya/internal/agent/catalog"
)

// AgentTint is an agent's color, or Miya's violet for one without.
func AgentTint(agent string) core.Color {
	if hex := catalog.For(agent).Icon.Color; hex != "" {
		if c, err := core.ParseColor(hex); err == nil {
			return c
		}
	}
	return core.RGB(0x7c6cf6)
}

var agentImages sync.Map // agent ID → data URI of its PNG

// AgentIcon is an agent's mark at size: its symbol in its color, or its
// picture.
func AgentIcon(agent string, size core.Points) core.Node {
	spec := catalog.For(agent).Icon
	tint := AgentTint(agent)
	if _, ok := LookupSVG(spec.SVG); spec.SVG != "" && ok {
		return Icon(spec.SVG, size, func() core.Color { return tint })
	}
	if len(spec.PNG) > 0 {
		uri, ok := agentImages.Load(agent)
		if !ok {
			uri, _ = agentImages.LoadOrStore(agent, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(spec.PNG))
		}
		return core.Image{Source: core.Src(uri.(string)), Label: agent,
			Style: core.Style{Width: core.Pt(size), Height: core.Pt(size), FlexShrink: 0}}
	}
	return Icon("message-circle", size, func() core.Color { return tint })
}
