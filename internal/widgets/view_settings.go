package widgets

import (
	fmt "fmt"
	math "math"
	strings "strings"
	sync "sync"

	ui "github.com/egoist/mygo/ui"
)

var ringMu sync.Mutex

var ringCache = map[string]*ui.SVG{}

// ringSVG draws a usage ring as an SVG in its own color, kept per percent.
func RingSVG(percent float64, color string) *ui.SVG {
	key := fmt.Sprintf("%.0f %s", math.Round(percent), color)
	ringMu.Lock()
	defer ringMu.Unlock()
	if s, ok := ringCache[key]; ok {
		return s
	}
	s := ui.MustParseSVG([]byte(strings.ReplaceAll(QuotaRing(math.Round(percent)), "#555555", color)))
	ringCache[key] = s
	return s
}

func QuotaRing(percent float64) string {
	p := math.Max(0, math.Min(100, percent))
	angle := p * 2 * math.Pi / 100
	x, y := 12+8*math.Sin(angle), 12-8*math.Cos(angle)
	arc := ""
	if p >= 100 {
		arc = `<circle cx="12" cy="12" r="8" fill="none" stroke="#555555" stroke-width="2.5"/>`
	} else if p > 0 {
		large := 0
		if p > 50 {
			large = 1
		}
		arc = fmt.Sprintf(`<path d="M12 4 A8 8 0 %d 1 %.3f %.3f" fill="none" stroke="#555555" stroke-width="2.5" stroke-linecap="round"/>`, large, x, y)
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">` + arc + `</svg>`
}
