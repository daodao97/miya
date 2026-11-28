package view

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"miya/internal/chat/model"
	"miya/internal/translate"
	"miya/internal/widgets"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// translation is the state of one reply's translation into one language.
type translation struct {
	// id names it in the link of its button; key where the agents run
	// (translate.Key).
	id      int
	key     string
	content string
	target  translate.Target
	text    string
	err     string
	pending bool
	// elsewhere says another window or phone is having it translated.
	elsewhere bool
	// shown says the translation, not the original, is on screen.
	shown bool
	// autoAsked says automatic translation asked for it once: going back
	// to the original, or failing, does not ask again by itself.
	autoAsked bool
	// lookedAt is the TranslationRevision, plus one, at which the view last
	// asked whether another device translated it.
	lookedAt uint64
}

// maxAutoTranslations is how many replies translate by themselves at once:
// a history full of replies to translate asks for a few at a time.
const maxAutoTranslations = 2

// translateScheme starts the destination of a reply's translate button: a
// link at the end of its last line, which the button's icon draws.
const translateScheme = "miya-translate:"

// translationFade is how a reply crosses to its translation and back.
var translationFade = ui.ElementTransition{
	Duration: 320 * time.Millisecond,
	Enter:    &ui.Motion{Opacity: 0, Blur: 3, Y: 4},
}

func (app *View) translatable() bool {
	return app.Translate != nil && widgets.TranslationEnabled() && (app.CanTranslate == nil || app.CanTranslate())
}

// translationFor returns the state of content's translation into target,
// made on first use.
func (app *View) translationFor(content string, target translate.Target) *translation {
	k := target.Tag + "\x00" + content
	if s := app.translations[k]; s != nil {
		return s
	}
	if app.translations == nil {
		app.translations, app.translationIDs = map[string]*translation{}, map[int]*translation{}
	}
	app.translationSerial++
	s := &translation{id: app.translationSerial, key: translate.Key(content, target.Tag), content: content, target: target}
	app.translations[k], app.translationIDs[s.id] = s, s
	return s
}

// withTranslateButton ends src with the button of s, on its last line
// where that is text; after a table or code, which a link would join or
// break, on a line of its own.
func withTranslateButton(src string, s *translation, label string) string {
	src = strings.TrimRight(src, " \t\n")
	last := src[strings.LastIndexByte(src, '\n')+1:]
	trimmed := strings.TrimSpace(last)
	sep := " "
	if strings.HasPrefix(trimmed, "|") || strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") ||
		strings.HasPrefix(last, "    ") || strings.HasPrefix(last, "\t") {
		sep = "\n\n"
	}
	return src + sep + "[](" + translateScheme + strconv.Itoa(s.id) + ` "` + strings.ReplaceAll(label, `"`, `'`) + `")`
}

// hookTranslateLinks makes the view's links to translateScheme draw the
// translate button and toggle their reply, leaving others to the host's
// LinkIcon and OnLink. Hosts set Md before the first frame.
func (app *View) hookTranslateLinks() {
	if app.translateHooked {
		return
	}
	app.translateHooked = true
	icon, open, color := app.Md.LinkIcon, app.Md.OnLink, app.Md.LinkColor
	app.Md.LinkColor = func(dest string) (markdown.Color, bool) {
		if strings.HasPrefix(dest, translateScheme) {
			// Next to invisible: the link has no text, and the line a link
			// shows under it on hover would underline the icon.
			return markdown.Color{A: 1}, true
		}
		if color != nil {
			return color(dest)
		}
		return markdown.Color{}, false
	}
	app.Md.LinkIcon = func(dest string, size float32) (markdown.InlineObject, bool) {
		if s, ok := app.translateLink(dest); ok {
			return app.translateIcon(s, size), true
		}
		if icon != nil {
			return icon(dest, size)
		}
		return markdown.InlineObject{}, false
	}
	app.Md.OnLink = func(dest string) {
		if s, ok := app.translateLink(dest); ok {
			app.toggleTranslation(s)
			return
		}
		if open != nil {
			open(dest)
		}
	}
}

func (app *View) translateLink(dest string) (*translation, bool) {
	rest, ok := strings.CutPrefix(dest, translateScheme)
	if !ok {
		return nil, false
	}
	id, err := strconv.Atoi(rest)
	if err != nil {
		return nil, false
	}
	s := app.translationIDs[id]
	return s, s != nil
}

// translateIcon draws the button of s: its icon after the last word, muted,
// in the accent color while the translation shows, pulsing while the agent
// translates.
func (app *View) translateIcon(s *translation, size float32) markdown.InlineObject {
	side := size * 0.95
	gap := size * 0.3
	return markdown.InlineObject{
		Width: gap + side, Height: side * 0.82, Depth: side * 0.18,
		Draw: func(p *ui.Painter, r ui.Rect) {
			color := widgets.Muted
			switch {
			case s.pending || s.elsewhere:
				p.AnimationFrameRate(20)
				t := float64(p.Now().UnixMilli()%1200) / 1200
				color = color.Alpha(float32(0.35 + 0.65*(0.5+0.5*math.Cos(2*math.Pi*t))))
			case s.shown && s.text != "":
				// Lighter once it did its work: the translation reads on its own.
				color = color.Alpha(0.55)
			}
			p.Icon(widgets.IconSVG("language"), ui.Rect{X: r.X + gap, Y: r.Y + (r.H-side)/2, W: side, H: side}, color)
		},
	}
}

// toggleTranslation answers a click on the button of s: show the
// translation, asking for it the first time, or the original again.
func (app *View) toggleTranslation(s *translation) {
	switch {
	case s.pending:
	case s.shown:
		s.shown = false
	case s.text != "":
		s.shown, s.err = true, ""
	default:
		app.startTranslation(s, nil)
	}
}

// startTranslation asks the agent for s, then shows it; done, if set, runs
// as the answer comes.
func (app *View) startTranslation(s *translation, done func()) {
	s.pending, s.err = true, ""
	app.Translate(app.C.ViewThread(), s.content, s.target.Tag, func(text string, err error) {
		s.pending = false
		if done != nil {
			done()
		}
		if err != nil {
			s.err = err.Error()
			return
		}
		s.text, s.shown = text, true
	})
}

// lookupTranslation shows the translation of s another device made, as
// the count of translations where the agents run changes: one translated
// on the desktop shows translated on the phone, and the other way round.
func (app *View) lookupTranslation(s *translation) {
	if app.LookupTranslations == nil || app.TranslationRevision == nil || s.text != "" || s.pending {
		return
	}
	at := app.TranslationRevision() + 1
	if s.lookedAt == at {
		return
	}
	key := s.key
	// A phone without its desktop asks again once it has it.
	if app.LookupTranslations([]string{key}, func(found map[string]string) {
		if text, ok := found[key]; ok && s.text == "" && !s.pending {
			s.text, s.shown, s.err = text, true, ""
		}
	}) {
		s.lookedAt = at
	}
}

// autoTranslate translates a reply that shows, once, when the device asks
// for it, a few at a time: the others wait for the next frame after one is
// done.
func (app *View) autoTranslate(s *translation) {
	if s.autoAsked || s.pending || s.text != "" || app.autoTranslating >= maxAutoTranslations {
		return
	}
	s.autoAsked = true
	app.autoTranslating++
	app.startTranslation(s, func() { app.autoTranslating-- })
}

// assistantText shows a reply. A finished one in another language than the
// device translates into ends with a button, on its last line, that
// switches it to that language and back. The reply's text keeps its place
// in the tree whether or not the button shows, so a selection survives a
// reply that turns translatable as it grows.
func (app *View) assistantText(c *ui.Context, m model.Message, streaming bool) {
	content := model.ImagePlaceholder.ReplaceAllString(m.Content, "")
	var state *translation
	if target := widgets.TranslationTarget(); !streaming && app.translatable() && translate.Needed(content, target.Tag) {
		app.hookTranslateLinks()
		state = app.translationFor(content, target)
		state.elsewhere = state.text == "" && !state.pending && app.TranslatingElsewhere != nil && app.TranslatingElsewhere(state.key)
		app.lookupTranslation(state)
		if widgets.Translation().Auto {
			app.autoTranslate(state)
		}
	}
	showing := state != nil && state.shown && state.text != ""
	ui.Column(c).FillWidth().MinWidth(0).Gap(2).Children(func() {
		body := ui.Column(c).FillWidth().MinWidth(0)
		if state != nil {
			translatingShimmer(c, body, state.pending || state.elsewhere)
		}
		body.Children(func() {
			switch {
			case showing:
				ui.Column(c.Key("translation")).FillWidth().MinWidth(0).Transition(translationFade).Children(func() {
					markdown.View(c, withTranslateButton(state.text, state, tr("Show original")), &app.Md)
				})
			case state != nil:
				label := fmt.Sprintf(tr("Translate to %s"), i18n.Autonym(state.target.Tag))
				src := withTranslateButton(content, state, label)
				if state.text == "" {
					markdown.View(c, src, &app.Md)
					return
				}
				// Back from the translation: the original fades in as it did.
				ui.Column(c.Key("original")).FillWidth().MinWidth(0).Transition(translationFade).Children(func() {
					markdown.View(c, src, &app.Md)
				})
			case streaming:
				markdown.Stream(c, content, &app.Md)
			default:
				markdown.View(c, content, &app.Md)
			}
		})
		if state != nil && state.err != "" {
			widgets.MutedText(c.Key("translate-error"), tr(state.err)).FontSize(12).MinWidth(0).Wrap()
		}
	})
}

// translatingShimmer shows that the agent is translating the text of body:
// it dims, and a band of light sweeps across it, word after word, until
// the translation comes. Without motion it only dims.
func translatingShimmer(c *ui.Context, body ui.Element, pending bool) {
	dim := body.Animate("dim", map[bool]float32{false: 0, true: 1}[pending], 240*time.Millisecond)
	if dim <= 0 {
		return
	}
	const low = 0.32
	base := 1 - (1-low)*dim
	if c.Preferences().ReduceMotion || !pending {
		body.Opacity(base)
		return
	}
	// The band crosses from before the start to past the end, so that it
	// enters and leaves the text.
	const half = 0.22
	at := -half + (1+2*half)*body.LoopFPS("shimmer", 1600*time.Millisecond, ui.Linear, 30)
	black := ui.RGB(0, 0, 0)
	clamp := func(x float32) float32 { return min(max(x, 0), 1) }
	stops := []ui.GradientStop{
		{Color: black.Alpha(base), At: 0},
		{Color: black.Alpha(base), At: clamp(at - half)},
		{Color: black, At: clamp(at)},
		{Color: black.Alpha(base), At: clamp(at + half)},
		{Color: black.Alpha(base), At: 1},
	}
	body.MaskLinearGradient(ui.LinearGradient{Angle: 105, Stops: stops})
}
