package desktopui

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
	qrcode "github.com/skip2/go-qrcode"

	"miya/internal/widgets"
)

// phoneSettings shows the QR code a phone scans to watch this desktop. With
// the background service, the service serves phones and this window shows
// its status and forwards the actions.
func (app *App) phoneSettings(c *ui.Context) {
	st, act, ok := app.C.PhoneControls()
	if !ok {
		// Phones talk to the background service; without it, say so rather
		// than leave the page blank.
		ui.Column(c).FillWidth().Gap(16).Children(func() {
			widgets.SettingsHeader(c, tr("Mobile connections"), "", nil)
			widgets.SettingsCard(c).AlignItems(ui.Center).Padding(32, 20).Gap(10).Children(func() {
				widgets.Icon(c, "device-mobile", 28, widgets.Faint)
				ui.Text(c, tr("Miya background service is not running. Open Miya first.")).FontSize(12).TextColor(widgets.Muted)
			})
		})
		return
	}
	ui.Column(c).FillWidth().Gap(16).Children(func() {
		help := tr("Scan with the Miya mobile app to continue conversations and handle approvals. Settings cannot be changed from your phone. Connections use Tailcat end-to-end encryption.")
		if app.C.RemoteHost() {
			help = tr("Scan the server's QR code with Miya on your phone. Desktop and mobile share the server's projects and conversations. Connections use Tailcat end-to-end encryption.")
		}
		widgets.SettingsHeader(c, tr("Mobile connections"), help, nil)
		widgets.SettingsCard(c).Padding(20).Gap(14).Children(func() {
			switch {
			case st.Busy:
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Spinner(c)
					widgets.MutedText(c, tr("Enabling mobile connections…"))
				})
			case st.Link == "":
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
					widgets.SettingLabel(c, tr("Disabled"), tr("Enable to generate a QR code, then scan it with your phone.")).Grow(1)
					if ui.PrimaryButton(c.Key("phone-start"), tr("Enable mobile connections")).Clicked() {
						act("start")
					}
				})
			default:
				ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(24).Children(func() {
					if qr := phoneQR(st.Link); len(qr) > 0 {
						// A QR code reads on white in either theme.
						ui.Box(c.Key("phone-qr-frame")).Shrink(0).Padding(10).Background(ui.Hex("#ffffff")).Border(1, widgets.Line).Radius(12).Children(func() {
							ui.Box(c.Key("phone-qr")).Label(tr("Mobile connection QR code")).Size(200, 200).Background(ui.Hex("#ffffff")).Draw(func(painter *ui.Painter, r ui.Rect) { paintQR(painter, r, qr) })
						})
					}
					ui.Column(c).Grow(1).Basis(240).MinWidth(0).Gap(14).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
							dot, status := widgets.Muted, tr("Waiting for your phone to scan…")
							if st.Peers > 0 {
								dot, status = widgets.Added, fmt.Sprintf(tr("%d phones connected"), st.Peers)
							}
							ui.Box(c).Size(8, 8).Radius(4).Background(dot).Shrink(0)
							ui.Text(c, status).FontSize(14).FontWeight(500)
						})
						access := tr("The connection code grants viewing access. Keep it private. Resetting requires your phone to scan again.")
						if app.C.RemoteHost() {
							access = tr("The server connection code lets desktop clients act as the server account. Share only with trusted devices. Resetting requires all devices to reconnect.")
						}
						ui.Text(c, access).TextColor(widgets.Muted).FontSize(12).LineHeight(1.5)
						ui.Row(c).Wrap().Gap(8).Children(func() {
							if widgets.SmallButton(c.Key("phone-copy"), tr("Copy connection code"), false).Clicked() {
								c.WriteClipboard(st.Link)
							}
							if widgets.SmallButton(c.Key("phone-reset"), tr("Reset QR code"), false).Clicked() {
								act("reset")
							}
							if widgets.SmallButton(c.Key("phone-stop"), tr("Close"), false).TextColor(widgets.Danger).Clicked() {
								act("stop")
							}
						})
					})
				})
			}
			if st.Message != "" {
				ui.Text(c, st.Message).TextColor(widgets.Danger).FontSize(12)
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
