package widgets

import (
	"embed"
	"encoding/base64"
	"net/url"
	"os"
	"strings"
	"sync"

	"miya/internal/agent"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/uiruntime"
)

//go:embed assets/icons/*.svg
var icons embed.FS

var Canvas = ui.Hex("#ffffff")

var Side = ui.Hex("#fafafa")

var Panel = ui.Hex("#ffffff")

var Ink = ui.Hex("#202020")

var Muted = ui.Hex("#858585")

var Line = ui.Hex("#e5e5e5")

var Hover = ui.Hex("#f0f0f0")

var Chosen = ui.Hex("#eeeeee")

var Bubble = ui.Hex("#f4f4f4")

var Danger = ui.Hex("#b42318")

var Failed = ui.Hex("#e58b84")

var Added = ui.Hex("#8bc7a1")

var Hunk = ui.Hex("#8eb6e8")

var CodeBg = ui.Hex("#202124")

var CodeInk = ui.Hex("#c7cad1")

var claudeInk = ui.Hex("#c46b4d")

var Shadow = ui.RGBA(0, 0, 0, 0.16)

const MonoFont = "Menlo"

func AppTheme() *ui.Theme { return ApplyDesktopPalette(false) }

var iconMu sync.Mutex

var iconCache = map[string]*ui.SVG{}

// svgIcon parses an embedded Tabler icon once. Icons take the text color.
func svgIcon(name string) *ui.SVG {
	iconMu.Lock()
	defer iconMu.Unlock()
	if s, ok := iconCache[name]; ok {
		return s
	}
	data, err := icons.ReadFile("assets/icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	s := ui.MustParseSVG(data)
	iconCache[name] = s
	return s
}

func Icon(c *ui.Context, name string, size float32, color ui.Color) ui.Element {
	return ui.Icon(c, svgIcon(name)).FontSize(size).TextColor(color).Shrink(0)
}

// agentImages keeps the decoded bitmap icons of agents, by agent ID.
var agentImages sync.Map

// agentIcon draws the icon an agent's adapter describes.
func AgentIcon(c *ui.Context, agent string, size float32) {
	spec := AgentSpec(agent)
	if spec.SVG != "" {
		Icon(c, spec.SVG, size, ui.Hex(spec.Color))
		return
	}
	src, ok := agentImages.Load(agent)
	if !ok {
		img, err := ui.DecodeImage(spec.PNG)
		if err != nil {
			return
		}
		src, _ = agentImages.LoadOrStore(agent, img)
	}
	ui.Image(c, src.(ui.ImageSource)).Size(size, size).Shrink(0)
}

// quiet is the app's borderless button: transparent until hovered.
func Quiet(c *ui.Context) ui.Element {
	b := ui.ButtonBase(c).Row().AlignItems(ui.Center).Justify(ui.Start).Gap(8).Height(32).PaddingX(9).Radius(7).
		TextColor(Ink).FontSize(12).Cursor(ui.CursorPointer)
	if b.Hovered() && !b.IsDisabled() {
		b.Background(Hover)
	}
	return b
}

// toolButton is a square icon button with a tooltip naming it.
func ToolButton(c *ui.Context, name, label string) ui.Element {
	return Quiet(c).Width(32).Justify(ui.Center).PaddingX(0).Label(label).Tooltip(label).Children(func() {
		Icon(c, name, 17, Muted)
	})
}

// card is the bordered white panel used by popovers and settings.
func Card(c *ui.Context) ui.Element {
	return ui.Column(c).Padding(14).Gap(10).Background(Panel).Border(1, Line).Radius(12)
}

func PopupPanel(panel ui.Element, width float32) {
	panel.Width(width).Padding(14).Gap(10).Background(Canvas).Border(1, Border).Radius(12).
		Shadow(0, 8, 24, 0, Shadow).Margin(8, 0, 0, 0)
}

func MutedText(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).TextColor(Muted).FontSize(12)
}

var ImageMu sync.Mutex

var ImageCache = map[string]ui.ImageSource{}

// loadImage decodes a message image (a file URL, a path or a data URI) once.
func LoadImage(src string) (ui.ImageSource, error) {
	ImageMu.Lock()
	if img, ok := ImageCache[src]; ok {
		ImageMu.Unlock()
		return img, nil
	}
	ImageMu.Unlock()
	var data []byte
	var err error
	switch {
	case strings.HasPrefix(src, "data:image/"):
		header, payload, ok := strings.Cut(src, ",")
		if !ok {
			return nil, os.ErrInvalid
		}
		if strings.HasSuffix(header, ";base64") {
			data, err = base64.StdEncoding.DecodeString(payload)
		} else {
			var decoded string
			decoded, err = url.PathUnescape(payload)
			data = []byte(decoded)
		}
	case strings.HasPrefix(src, "file://"):
		var u *url.URL
		if u, err = url.Parse(src); err == nil {
			data, err = os.ReadFile(u.Path)
		}
	default:
		data, err = os.ReadFile(src)
	}
	if err != nil {
		return nil, err
	}
	img, err := ui.DecodeImage(data)
	if err != nil {
		return nil, err
	}
	ImageMu.Lock()
	ImageCache[src] = img
	ImageMu.Unlock()
	return img, nil
}

// cachedImage returns an image already decoded, decoding it on a goroutine
// otherwise so that a frame never waits for the disk.
func CachedImage(src string) ui.ImageSource {
	ImageMu.Lock()
	img, ok := ImageCache[src]
	ImageMu.Unlock()
	if ok {
		return img
	}
	go func() {
		if _, err := LoadImage(src); err == nil {
			uiruntime.Invalidate()
		} else {
			ImageMu.Lock()
			ImageCache[src] = nil
			ImageMu.Unlock()
		}
	}()
	return nil
}

// workingLabel shows a running label with its icon, still. A wave once
// flowed through it, and an animated label keeps the whole window building
// frames while a conversation runs: a third of a core at the display's
// rate, a fifth at 24 frames a second. The label changes once a second.
func WorkingLabel(c *ui.Context, symbol, s string) ui.Element {
	row := ui.Row(c).AlignItems(ui.Center).Gap(8).Label(s)
	row.Children(func() {
		Icon(c, symbol, 15, Muted).Shrink(0)
		ui.Text(c, s).MinWidth(0).MaxLines(2).FontSize(12).TextColor(Muted)
	})
	return row
}

func AgentSpec(id string) agent.Icon {
	spec := agent.Icon{PNG: appmodel.CodexAgentPNG}
	if id == appmodel.AgentClaude {
		spec = agent.Icon{SVG: "claude-code", Color: "#c46b4d"}
	}
	return spec
}
