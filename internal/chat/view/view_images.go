package view

import (
	"github.com/egoist/mygo/ui"

	"miya/internal/widgets"
)

func (app *View) imageThumbnail(c *ui.Context, src string) {
	load := app.ImagePreview
	if load == nil {
		load = widgets.CachedImage
	}
	label := tr("Image preview")
	if img := load(src); img != nil {
		button := ui.ButtonBase(c).Size(80, 80).Radius(8).Clip().Role(ui.RoleButton).Cursor(ui.CursorPointer).Label(label).Tooltip(label)
		button.Children(func() {
			ui.Image(c, img).Size(80, 80).Fit(ui.Cover)
		})
		if button.Clicked() {
			app.previewImage = img
		}
		return
	}
	ui.Box(c).Size(80, 80).Radius(8).Background(widgets.Bubble).Label(label).Center().Children(func() {
		widgets.Icon(c, "file", 24, widgets.Muted)
	})
}

func (app *View) imageDialog(c *ui.Context) {
	const margin, closeSize, gap = 16, 44, 8
	top, right, bottom, left := c.SafeArea()
	bounds := c.Root().Bounds()
	maxWidth := max(0, bounds.W-left-right-2*margin)
	maxHeight := max(0, bounds.H-top-bottom-2*margin-closeSize-gap)
	img := app.previewImage
	w, h := ui.ImageSize(img)
	scale := min(maxWidth/max(w, 1), maxHeight/max(h, 1))

	open := true
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.78)).Padding(top+margin, right+margin, bottom+margin, left+margin)
		panel.Column().AlignItems(ui.End).Gap(gap).Label(tr("Image preview"))
		label := tr("Close")
		closeButton := widgets.Quiet(c.Key("close")).Size(closeSize, closeSize).PaddingX(0).Justify(ui.Center).
			Radius(closeSize / 2).Background(widgets.Panel).Role(ui.RoleButton).Label(label).Tooltip(label).AutoFocus()
		closeButton.Children(func() { widgets.Icon(c, "x", 20, widgets.Ink) })
		if closeButton.Clicked() {
			open = false
			c.After(0)
		}
		ui.Image(c.Key("image"), img).Size(w*scale, h*scale).Fit(ui.Contain).Label(tr("Image preview"))
	})
	if !open {
		app.previewImage = nil
	}
}
