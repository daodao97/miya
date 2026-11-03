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
	ui.Column(c).FillWidth().Gap(16).Children(func() {
		settingsHeader(c, "手机连接", "用 Miya 手机端扫码连接。手机可以继续对话和处理审批，不能修改设置。连接经 Tailcat 端到端加密。", nil)
		settingsCard(c).Padding(20).Gap(14).Children(func() {
			switch {
			case st.Busy:
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Spinner(c)
					mutedText(c, "正在开启手机连接…")
				})
			case st.Link == "":
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
					settingLabel(c, "未开启", "开启后生成二维码，用手机扫码即可连接。").Grow(1)
					if ui.PrimaryButton(c.Key("phone-start"), "开启手机连接").Clicked() {
						act("start")
					}
				})
			default:
				ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(24).Children(func() {
					if qr := phoneQR(st.Link); len(qr) > 0 {
						// A QR code reads on white in either theme.
						ui.Box(c.Key("phone-qr-frame")).Shrink(0).Padding(10).Background(ui.Hex("#ffffff")).Border(1, line).Radius(12).Children(func() {
							ui.Box(c.Key("phone-qr")).Label("手机连接二维码").Size(200, 200).Background(ui.Hex("#ffffff")).Draw(func(painter *ui.Painter, r ui.Rect) { paintQR(painter, r, qr) })
						})
					}
					ui.Column(c).Grow(1).Basis(240).MinWidth(0).Gap(14).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							dot, status := muted, "等待手机扫码…"
							if st.Peers > 0 {
								dot, status = added, fmt.Sprintf("已连接 %d 台手机", st.Peers)
							}
							ui.Box(c).Size(8, 8).Radius(4).Background(dot).Shrink(0)
							ui.Text(c, status).FontSize(14).FontWeight(500)
						})
						ui.Text(c, "连接码等同于查看权限，请勿分享。重置后，手机需要重新扫码。").TextColor(muted).FontSize(12).LineHeight(1.5)
						ui.Row(c).Wrap().Gap(8).Children(func() {
							if smallButton(c.Key("phone-copy"), "复制连接码", false).Clicked() {
								c.WriteClipboard(st.Link)
							}
							if smallButton(c.Key("phone-reset"), "重置二维码", false).Clicked() {
								act("reset")
							}
							if smallButton(c.Key("phone-stop"), "关闭", false).TextColor(danger).Clicked() {
								act("stop")
							}
						})
					})
				})
			}
			if st.Message != "" {
				ui.Text(c, st.Message).TextColor(danger).FontSize(12)
			}
		})
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
