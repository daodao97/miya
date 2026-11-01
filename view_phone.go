package main

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
	qrcode "github.com/skip2/go-qrcode"
)

// phoneSettings shows the QR code a phone scans to watch this desktop. With
// the background service, the service serves phones and this window shows
// its status and forwards the actions.
func (app *chatApp) phoneSettings(c *ui.Context) {
	var st phoneStatus
	var act func(string)
	switch {
	case desktopPhone != nil:
		st, act = desktopPhone.status(), desktopPhone.do
	case app.c.remote != nil && app.c.mirror != nil:
		st, act = app.c.mirror.phone, func(a string) { app.c.forward("phone", a) }
	default:
		return
	}
	card(c).Padding(24).Gap(16).Children(func() {
		ui.Text(c, "手机连接").FontWeight(600)
		ui.Text(c, "用 Miya 手机端扫码连接。手机可以继续对话和处理审批，不能修改设置。连接经 Tailcat 端到端加密。").TextColor(muted).FontSize(12).LineHeight(1.5)
		switch {
		case st.Busy:
			ui.Row(c).Gap(8).Children(func() {
				ui.Spinner(c)
				mutedText(c, "正在开启手机连接…")
			})
		case st.Link == "":
			if quiet(c.Key("phone-start")).AlignSelf(ui.Start).Border(1, line).Padding(0, 14).Children(func() { ui.Text(c, "开启手机连接") }).Clicked() {
				act("start")
			}
		default:
			ui.Column(c).AlignSelf(ui.Start).AlignItems(ui.Center).Gap(12).Children(func() {
				if qr := phoneQR(st.Link); len(qr) > 0 {
					// A QR code reads on white in either theme.
					ui.Box(c.Key("phone-qr")).Label("手机连接二维码").Size(240, 240).Background(ui.Hex("#ffffff")).Draw(func(painter *ui.Painter, r ui.Rect) { paintQR(painter, r, qr) })
				}
				if st.Peers > 0 {
					mutedText(c, fmt.Sprintf("已连接 %d 台手机", st.Peers))
				} else {
					mutedText(c, "等待手机扫码…")
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				if quiet(c.Key("phone-copy")).Border(1, line).Padding(0, 12).Children(func() { ui.Text(c, "复制连接码") }).Clicked() {
					c.WriteClipboard(st.Link)
				}
				if quiet(c.Key("phone-reset")).Border(1, line).Padding(0, 12).Children(func() { ui.Text(c, "重置二维码") }).Clicked() {
					act("reset")
				}
				if quiet(c.Key("phone-stop")).Border(1, line).Padding(0, 12).Children(func() { ui.Text(c, "关闭") }).Clicked() {
					act("stop")
				}
			})
			ui.Text(c, "连接码等同于查看权限，请勿分享。重置后，手机需要重新扫码。").TextColor(muted).FontSize(12)
		}
		if st.Message != "" {
			ui.Text(c, st.Message).TextColor(danger).FontSize(12)
		}
	})
}

// phoneQR encodes the link once, not on every frame.
var qrCache struct {
	link string
	qr   [][]bool
}

func phoneQR(link string) [][]bool {
	if link == "" {
		return nil
	}
	if qrCache.link != link {
		qrCache.link, qrCache.qr = link, nil
		if code, err := qrcode.New(link, qrcode.Medium); err == nil {
			qrCache.qr = code.Bitmap()
		}
	}
	return qrCache.qr
}

func paintQR(p *ui.Painter, r ui.Rect, code [][]bool) {
	if len(code) == 0 {
		return
	}
	p.Fill(r, ui.Hex("#ffffff"), 0)
	size := float32(math.Floor(float64(min(r.W, r.H)*p.Scale()/float32(len(code))))) / p.Scale()
	x, y := r.X+(r.W-size*float32(len(code)))/2, r.Y+(r.H-size*float32(len(code)))/2
	for row, cells := range code {
		for col, on := range cells {
			if on {
				p.Fill(ui.Rect{X: x + float32(col)*size, Y: y + float32(row)*size, W: size, H: size}, ui.Hex("#000000"), 0)
			}
		}
	}
}
