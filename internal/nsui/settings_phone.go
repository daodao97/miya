package nsui

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	qrcode "github.com/skip2/go-qrcode"
	svcproto "miya/internal/service/protocol"
)

func phoneSettings(cx *core.Scope, c Host) []ui.SettingsGroup {
	return []ui.SettingsGroup{{ID: "phone", Title: tr("Mobile connections"), Items: []ui.SettingsItem{{ID: "connections", Title: tr("Mobile connections"), Content: core.Dynamic(func(cx *core.Scope) core.Node {
		st := Live(func() svcproto.PhoneStatus { st, _, _ := c.PhoneControls(); return st })()
		_, act, ok := c.PhoneControls()
		client := c.State().Remote
		action := func(op string) func() {
			return func() {
				if c.State().Remote == client && act != nil {
					act(op)
				}
			}
		}
		var nodes []core.Node
		text := func(s string) { nodes = append(nodes, settingsText(cx, func() string { return s })) }
		if !ok {
			text(tr("Miya background service is not running. Open Miya first."))
			return settingsColumn(nodes...)
		}
		help := tr("Scan with the Miya mobile app to continue conversations and handle approvals. Settings cannot be changed from your phone. Connections use Tailcat end-to-end encryption.")
		if c.RemoteHost() {
			help = tr("Scan the server's QR code with Miya on your phone. Desktop and mobile share the server's projects and conversations. Connections use Tailcat end-to-end encryption.")
		}
		text(help)
		switch {
		case st.Busy:
			text(tr("Enabling mobile connections…"))
		case st.Link == "":
			text(tr("Enable to generate a QR code, then scan it with your phone."))
			nodes = append(nodes, settingsButton("phone-start", tr("Enable mobile connections"), action("start"), nil))
		default:
			nodes = append(nodes, phoneQRCode(st.Link))
			status := tr("Waiting for your phone to scan…")
			if st.Peers > 0 {
				status = fmt.Sprintf(tr("%d phones connected"), st.Peers)
			}
			text(status)
			access := tr("The connection code grants viewing access. Keep it private. Resetting requires your phone to scan again.")
			if c.RemoteHost() {
				access = tr("The server connection code lets desktop clients act as the server account. Share only with trusted devices. Resetting requires all devices to reconnect.")
			}
			text(access)
			nodes = append(nodes, settingsActions(settingsButton("phone-copy", tr("Copy connection code"), func() { cx.SetClipboard(st.Link) }, nil), settingsButton("phone-reset", tr("Reset QR code"), action("reset"), nil), settingsButton("phone-stop", tr("Close"), action("stop"), nil)))
		}
		if st.Message != "" {
			text(st.Message)
		}
		return settingsColumn(nodes...)
	})}}}}
}

// An SVG keeps QR modules crisp in either appearance, without platform drawing
// callbacks. Include its white quiet zone even on a dark settings surface.
func phoneQRCode(link string) core.Node {
	qr, err := qrcode.New(link, qrcode.Medium)
	if err != nil {
		return core.Group(nil)
	}
	code := qr.Bitmap()
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="220" height="220" viewBox="0 0 %d %d"><rect width="100%%" height="100%%" fill="#ffffff"/><path fill="#000000" d="`, len(code), len(code))
	for y, row := range code {
		for x, on := range row {
			if on {
				fmt.Fprintf(&svg, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	svg.WriteString(`"/></svg>`)
	return core.SVGView{SVG: core.MustParseSVG([]byte(svg.String())), Label: tr("Mobile connection QR code"), TestID: "phone-qr", Style: core.Style{Width: core.Pt(220), Height: core.Pt(220), AlignSelf: core.AlignStart}}
}
