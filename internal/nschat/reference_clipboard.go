package nschat

import (
	"strings"

	re "github.com/nsgo-dev/nsgo/base/richeditor"
	"github.com/nsgo-dev/nsgo/core"
	"miya/internal/chat/model"
)

// adaptEditorSink installs lossless clipboard actions on the native sink and
// the visible editor's native popup menu. NSGo's rich view does not yet expose
// these props itself. Keep this adapter scoped to its public Render result.
func (r ReferenceEditor) adaptEditorSink(cx *core.Scope, root *core.View, sink core.Input, s *re.Session, snapshot func() func() bool) core.Input {
	in := r.Input
	pasteFiles := func(event core.PasteEvent) {
		if s.Editable() && in.OnPaste != nil {
			for i := range event.Files {
				if event.Files[i].Name == "" && event.Files[i].MIME == "image/png" {
					event.Files[i].Name = "pasted.png"
				}
			}
			in.OnPaste(event)
		}
	}
	copyText := func(cut bool) {
		text, _ := s.Copy()
		if text != "" {
			cx.SetClipboard(text)
		}
		if cut && s.Editable() {
			s.Run(re.DeleteSelection)
		}
	}
	paste := func(text string) {
		if !s.Editable() {
			return
		}
		var commands []model.ComposerChoice
		if r.Commands != nil {
			commands = r.Commands()
		}
		var files []string
		if r.Files != nil {
			files = r.Files()
		}
		nodes := editorNodes(s.State().Schema, text, editorTokens(text+" ", r.ReferenceTitle, commands, files))
		tr := s.State().Tr().ReplaceSelection(re.NewSlice(re.FragmentFrom(nodes...), 0, 0))
		s.Dispatch(re.CloseHistory(tr).ScrollIntoView())
	}

	readClipboard := func() {
		if !s.Editable() {
			return
		}
		valid := snapshot()

		cx.ReadClipboardFiles(func(uris []string) {
			if !valid() {
				return
			}
			if len(uris) > 0 {
				pasteFiles(core.PasteEvent{URIs: uris})
				return
			}
			cx.ReadClipboardImage(func(img core.ClipboardImage, ok bool, err error) {
				if !valid() {
					return
				}
				if ok {
					pasteFiles(core.PasteEvent{Files: []core.PastedFile{{Name: "pasted.png", MIME: "image/png", Data: img.PNG}}})
					return
				}
				if err != nil {
					return
				}
				cx.ReadClipboard(func(text string) {
					if valid() {
						paste(text)
					}
				})
			})
		})
	}

	sink.OnEditAction = func(action core.EditAction) {
		switch action {
		case core.EditCopy:
			copyText(false)
		case core.EditCut:
			copyText(true)
		case core.EditSelectAll:
			s.Run(re.SelectAll)
		}
	}
	sink.OnPaste = func(e core.PasteEvent) {
		if len(e.Files) > 0 || len(e.URIs) > 0 {
			pasteFiles(e)
		} else {
			paste(e.Text)
		}
	}
	actions := []struct {
		Label   string
		OnPress func()
	}{
		{Label: tr("Copy"), OnPress: func() { copyText(false) }},
		{Label: tr("Cut"), OnPress: func() { copyText(true) }},
		{Label: tr("Paste"), OnPress: func() { readClipboard() }},
		{Label: tr("Copy session link"), OnPress: func() {
			text, _ := s.Copy()
			ms := model.ThreadMentions(text)
			if len(ms) == 1 {
				cx.SetClipboard(ms[0].Ref.URL())
			}
		}},
		{Label: tr("Paste as conversation reference"), OnPress: func() {
			if !s.Editable() {
				return
			}
			valid := snapshot()
			cx.ReadClipboard(func(text string) {
				if !valid() {
					return
				}
				if ref, ok := model.ParseThreadURL(strings.TrimSpace(text)); ok {
					title := ""
					if r.ReferenceTitle != nil {
						title = r.ReferenceTitle(ref)
					}
					paste(model.ReferenceText(ref, title))
				} else if r.OnInvalidReference != nil {
					r.OnInvalidReference()
				}
			})
		}},
		{Label: tr("Select all"), OnPress: func() { s.Run(re.SelectAll) }},
	}
	root.OnContextMenu = func(e core.ContextMenuEvent) {
		text, _ := s.Copy()
		mentions := model.ThreadMentions(text)
		items := make([]core.MenuItem, 0, len(actions))
		for _, action := range actions {
			disabled := false
			switch action.Label {
			case tr("Copy"):
				disabled = s.State().Selection.Empty()
			case tr("Cut"):
				disabled = s.State().Selection.Empty() || !s.Editable()
			case tr("Paste"), tr("Paste as conversation reference"):
				disabled = !s.Editable()
			case tr("Copy session link"):
				disabled = len(mentions) != 1
			}
			items = append(items, core.MenuItem{Label: action.Label, Disabled: disabled, OnSelect: func() { action.OnPress(); s.Focus() }})
		}
		cx.PopupMenu(items, e.PagePosition, nil)
	}
	sink.ReadonlyFunc = func() bool { return in.Readonly || in.ReadonlyFunc != nil && in.ReadonlyFunc() }
	sink.Disabled = in.Disabled
	return sink
}
