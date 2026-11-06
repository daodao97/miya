package mobile

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/widgets"
)

// mobileTextField supplies a visible paste action for the custom-drawn
// inputs, including when iOS does not offer its selection menu.
type mobileTextField struct {
	caret   int
	pending bool
}

func (f *mobileTextField) restore(input ui.Element) {
	if f.pending {
		input.SetTextSelection(f.caret, f.caret)
		f.pending = false
	}
}

func (f *mobileTextField) paste(c *ui.Context, input ui.Element, value *string, multiline bool) {
	if input.Composing() {
		return
	}
	text := strings.ReplaceAll(c.ReadClipboard(), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if !multiline {
		text = strings.ReplaceAll(text, "\n", "")
	}
	if text == "" {
		return
	}
	start, end := input.TextSelection()
	runes := []rune(*value)
	start, end = min(start, len(runes)), min(end, len(runes))
	*value = string(runes[:start]) + text + string(runes[end:])
	f.caret, f.pending = start+len([]rune(text)), true
	input.Focus()
	c.Invalidate()
}

func (m *App) taskStatus(c *ui.Context) {
	t := m.Viewer.Current()
	p := m.Pal
	widgets.TaskStatusBar(c, t, len(m.ApprovalsFor(t.ServerID)) > 0, widgets.TaskPalette{Surface: p.surface, Border: p.line, Ink: p.ink, Muted: p.muted}, func(id, action string) {
		if m.Conn == nil || !m.Connected {
			m.SendErr = tr("Not connected to desktop")
			return
		}
		r := m.ThreadRequest("task", t.ID)
		r.Text = id + " " + action
		go m.Conn.Send(r)
	})
}
