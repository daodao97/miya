package nschat

import (
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/nswidgets"
)

type imagePreview struct {
	uri      string
	resolved bool
}

func (v View) resolveImage(cx *core.Scope, raw string) func() string {
	source := func() string { return raw }
	if v.ImageSource != nil {
		source = v.ImageSource(cx, raw)
	}
	return func() string {
		if path := source(); path != "" {
			return imageURI(path)
		}
		return ""
	}
}

func (v View) previewMarkdownImage(im textview.Image) {
	v.preview.Set(imagePreview{uri: im.Src})
}

func (v View) imageDialog(cx *core.Scope) core.Node {
	const margin, closeSize core.Points = 16, 44
	close := func() { v.preview.Set(imagePreview{}) }
	colors := nswidgets.ColorsOf(cx)
	return base.Dialog{
		Open: func() bool { return v.preview.Get().uri != "" },
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				close()
			}
		},
		TestID: "message-image-preview", Label: tr("Image preview"),
		Presentation: core.ModalFullScreen, Animation: core.ModalNone,
		BackdropTestID: "message-image-backdrop", BackdropStyle: core.Style{Background: core.RGBA(0, 0.78)},
		PopupStyleFunc: func() core.Style {
			available, safe := cx.Env().Window(), core.Insets(cx)
			return core.Style{Width: core.Pt(max(0, available.Width-safe.Left-safe.Right-2*margin)),
				Height: core.Pt(max(0, available.Height-safe.Top-safe.Bottom-2*margin)), Gap: 8}
		},
		LayerStyle: core.Style{PaddingEnv: core.EnvEdges{Top: core.EnvSafeArea, Right: core.EnvSafeArea,
			Bottom: core.EnvSafeArea, Left: core.EnvSafeArea}},
		Children: []core.Node{
			base.Button{TestID: "message-image-close", Label: tr("Close"), AutoFocus: true,
				OnPress: func(core.PressEvent) { close() },
				Style: core.Style{AlignSelf: core.AlignEnd, Width: core.Pt(closeSize), Height: core.Pt(closeSize),
					CornerRadius: closeSize / 2, FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
				StyleFunc: func() core.Style { return core.Style{Background: colors.Panel()} },
				Children:  []core.Node{nswidgets.Icon("x", 20, colors.Ink)}},
			core.Dynamic(func(cx *core.Scope) core.Node {
				picture := v.preview.Get()
				if picture.uri == "" {
					return nil
				}
				source := func() string { return picture.uri }
				if !picture.resolved {
					source = v.resolveImage(cx, picture.uri)
				}
				return core.Image{TestID: "message-image-full", Label: tr("Image preview"), ResizeMode: core.Contain,
					SourceFunc: func() core.ImageSource { return core.Src(source()) },
					Style:      core.Style{Width: core.Pct(100), FlexGrow: 1, MinHeight: core.Pt(0)}}
			}),
		},
	}
}
