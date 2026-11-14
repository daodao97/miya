package nswidgets

import "github.com/nsgo-dev/nsgo/core"

// Stack lays nodes out in a column gap apart, except that a node with
// nothing to show takes neither room nor a gap. A flex gap is kept around
// views of no height, and the hidden status, queue and request slots above
// the composer added up to push it away from what it follows.
func Stack(cx *core.Scope, gap core.Points, nodes ...core.Node) core.Node {
	heights := make([]*core.Signal[core.Points], len(nodes))
	for i := range heights {
		heights[i] = core.NewSignal(cx, core.Points(0))
	}
	children := make([]core.Node, len(nodes))
	for i, n := range nodes {
		i := i
		children[i] = core.View{OnLayout: func(b core.Bounds) { heights[i].Set(b.Size.Height) },
			StyleFunc: func() core.Style {
				top := core.Points(0)
				if heights[i].Get() > 0 {
					for _, h := range heights[:i] {
						if h.Get() > 0 {
							top = gap
							break
						}
					}
				}
				return core.Style{MinWidth: core.Pt(0), Margin: core.Edges{Top: top}}
			}, Children: []core.Node{n}}
	}
	return core.View{Style: core.Style{MinWidth: core.Pt(0)}, Children: children}
}
