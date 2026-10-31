package main

import (
	"embed"
	"encoding/base64"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/icons/*.svg
var icons embed.FS

var (
	canvas    = ui.Hex("#ffffff")
	side      = ui.Hex("#fafafa")
	panel     = ui.Hex("#ffffff")
	ink       = ui.Hex("#202020")
	muted     = ui.Hex("#858585")
	line      = ui.Hex("#e5e5e5")
	hover     = ui.Hex("#f0f0f0")
	chosen    = ui.Hex("#eeeeee")
	bubble    = ui.Hex("#f4f4f4")
	danger    = ui.Hex("#b42318")
	failed    = ui.Hex("#e58b84")
	added     = ui.Hex("#8bc7a1")
	hunk      = ui.Hex("#8eb6e8")
	codeBg    = ui.Hex("#202124")
	codeInk   = ui.Hex("#c7cad1")
	claudeInk = ui.Hex("#c46b4d")
	shadow    = ui.RGBA(0, 0, 0, 0.16)
)

const monoFont = "Menlo"

func appTheme() *ui.Theme { return applyDesktopPalette(false) }

var (
	iconMu    sync.Mutex
	iconCache = map[string]*ui.SVG{}
)

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

func icon(c *ui.Context, name string, size float32, color ui.Color) ui.Element {
	return ui.Icon(c, svgIcon(name)).FontSize(size).TextColor(color).Shrink(0)
}

// agentImages keeps the decoded bitmap icons of agents, by agent ID.
var agentImages sync.Map

// agentIcon draws the icon an agent's adapter describes.
func agentIcon(c *ui.Context, agent string, size float32) {
	spec := backendFor(agent).Icon()
	if spec.SVG != "" {
		icon(c, spec.SVG, size, ui.Hex(spec.Color))
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
func quiet(c *ui.Context) ui.Element {
	b := ui.ButtonBase(c).Row().AlignItems(ui.Center).Justify(ui.Start).Gap(8).Height(32).PaddingX(9).Radius(7).
		TextColor(ink).FontSize(12).Cursor(ui.CursorPointer)
	if b.Hovered() && !b.IsDisabled() {
		b.Background(hover)
	}
	return b
}

// toolButton is a square icon button with a tooltip naming it.
func toolButton(c *ui.Context, name, label string) ui.Element {
	return quiet(c).Width(32).Justify(ui.Center).PaddingX(0).Label(label).Tooltip(label).Children(func() {
		icon(c, name, 17, muted)
	})
}

// card is the bordered white panel used by popovers and settings.
func card(c *ui.Context) ui.Element {
	return ui.Column(c).Padding(14).Gap(10).Background(panel).Border(1, line).Radius(12)
}

func popupPanel(panel ui.Element, width float32) {
	panel.Width(width).Padding(14).Gap(10).Background(canvas).Border(1, border).Radius(12).
		Shadow(0, 8, 24, 0, shadow).Margin(8, 0, 0, 0)
}

func mutedText(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).TextColor(muted).FontSize(12)
}

var (
	imageMu    sync.Mutex
	imageCache = map[string]ui.ImageSource{}
)

// loadImage decodes a message image (a file URL, a path or a data URI) once.
func loadImage(src string) (ui.ImageSource, error) {
	imageMu.Lock()
	if img, ok := imageCache[src]; ok {
		imageMu.Unlock()
		return img, nil
	}
	imageMu.Unlock()
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
	imageMu.Lock()
	imageCache[src] = img
	imageMu.Unlock()
	return img, nil
}

// cachedImage returns an image already decoded, decoding it on a goroutine
// otherwise so that a frame never waits for the disk.
func cachedImage(src string) ui.ImageSource {
	imageMu.Lock()
	img, ok := imageCache[src]
	imageMu.Unlock()
	if ok {
		return img
	}
	go func() {
		if _, err := loadImage(src); err == nil {
			invalidate()
		} else {
			imageMu.Lock()
			imageCache[src] = nil
			imageMu.Unlock()
		}
	}()
	return nil
}

// waveStops is the gradient that flows through a running label: lively hues
// at one lightness and a low chroma, readable on white without glaring.
var waveStops = []ui.Color{
	ui.Oklch(0.64, 0.13, 25),  // coral
	ui.Oklch(0.68, 0.12, 75),  // amber
	ui.Oklch(0.64, 0.12, 145), // green
	ui.Oklch(0.62, 0.11, 230), // sky
	ui.Oklch(0.60, 0.12, 300), // violet
	ui.Oklch(0.64, 0.13, 25),
}

// waveColor is the gradient's color at x, from 0 to 1, wrapping around.
func waveColor(x float64) ui.Color {
	x -= math.Floor(x)
	n := float64(len(waveStops) - 1)
	i := int(x * n)
	if i >= len(waveStops)-1 {
		i = len(waveStops) - 2
	}
	return waveStops[i].Mix(waveStops[i+1], float32(x*n-float64(i)))
}

// waveText shows a running label with its icon: a color gradient flows
// through the text and a bright crest sweeps across it, the icon taking
// the gradient's color. The text is one rich text, so spaces keep their width.
func waveText(c *ui.Context, symbol, s string) ui.Element {
	runes := []rune(s)
	row := ui.Row(c).AlignItems(ui.Center).Gap(8).Label(s)
	span := float64(len(runes) + 10)
	phase := float64(row.Loop("wave", time.Duration(span*70)*time.Millisecond, ui.Linear))
	flow := float64(row.Loop("flow", 3*time.Second, ui.Linear))
	crest := phase*span - 5
	spans := make([]ui.Span, len(runes))
	for i, r := range runes {
		d := float64(i) - crest
		glow := math.Exp(-d * d / 10)
		color := muted.Mix(waveColor(float64(i)/float64(len(runes)+1)-flow), float32(0.8+0.2*glow))
		spans[i] = ui.Span{Text: string(r), Color: color}
	}
	row.Children(func() {
		icon(c, symbol, 15, waveColor(-flow))
		ui.RichText(c, spans...).FontSize(12)
	})
	return row
}
