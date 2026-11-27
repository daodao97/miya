package nschat

import (
	"strings"
	"unicode/utf16"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

// referenceText keeps backend identities in the draft while a native input
// shows short labels. Edits touching a reference replace that reference as one
// object; edits around it retain its original wire spelling and title snapshot.
func referenceText(raw string, title func(model.ThreadRef) string) string {
	var b strings.Builder
	end := 0
	for _, m := range model.Mentions(raw) {
		b.WriteString(raw[end:m.Start])
		b.WriteString(mentionLabel(m.Part, title))
		end = m.End
	}
	b.WriteString(raw[end:])
	return b.String()
}
func mentionLabel(p model.MessagePart, title func(model.ThreadRef) string) string {
	name := p.TitleSnapshot
	if p.Type == "thread_ref" {
		if name == "" && p.Ref != nil && title != nil {
			name = title(*p.Ref)
		}
		return "@" + name
	}
	if name == "" {
		name = model.AgentName(p.Agent)
	}
	return "#" + name
}
func referenceEdit(raw, value string, title func(model.ThreadRef) string) string {
	before := referenceText(raw, title)
	if before == value {
		return raw
	}
	// Common ranges are measured in runes so multi-byte labels never split.
	a, b := []rune(before), []rune(value)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	i, j := len(a), len(b)
	for i > prefix && j > prefix && a[i-1] == b[j-1] {
		i--
		j--
	}
	start, end := len(string(a[:prefix])), len(string(a[:i]))
	rawStart, rawEnd := referenceRange(raw, start, end, title)
	return raw[:rawStart] + string(b[prefix:j]) + raw[rawEnd:]
}
func referenceRange(raw string, start, end int, title func(model.ThreadRef) string) (int, int) {
	rawStart, rawEnd := start, end
	offset := 0
	for _, m := range model.Mentions(raw) {
		label := mentionLabel(m.Part, title)
		vs, ve := m.Start+offset, m.Start+offset+len(label)
		if start >= ve {
			rawStart += m.End - m.Start - len(label)
		} else if start > vs {
			rawStart = m.Start
		}
		if end >= ve {
			rawEnd += m.End - m.Start - len(label)
		} else if end > vs {
			rawEnd = m.End
		}
		offset += len(label) - (m.End - m.Start)
	}
	return rawStart, rawEnd
}
func utf16ByteOffset(text string, units int) int {
	n := 0
	for i, r := range text {
		if n >= units {
			return i
		}
		n += utf16.RuneLen(r)
	}
	return len(text)
}
func (v View) referenceChips(cx *core.Scope, text func() string, remove func(string)) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6}, Children: []core.Node{
		core.Each(func() []model.MessagePart {
			var out []model.MessagePart
			seen := map[string]bool{}
			for _, m := range model.Mentions(text()) {
				key := referenceKey(m.Part)
				if !seen[key] {
					seen[key] = true
					out = append(out, m.Part)
				}
			}
			return out
		}, referenceKey, func(cx *core.Scope, get func() model.MessagePart, _ func() int) core.Node {
			key := core.Untracked(cx, func() string { return referenceKey(get()) })
			children := []core.Node{core.Text{ContentFunc: func() string { return mentionLabel(get(), v.ReferenceTitle) }, Style: core.Style{FontSize: 12}}}
			if remove != nil {
				children = append(children, core.View{Role: core.RoleButton, Label: tr("Remove attachment"), TestID: "remove-reference-" + key, OnPress: func(core.PressEvent) {
					var b strings.Builder
					raw := text()
					end := 0
					for _, m := range model.Mentions(raw) {
						b.WriteString(raw[end:m.Start])
						if referenceKey(m.Part) != key {
							b.WriteString(raw[m.Start:m.End])
						}
						end = m.End
					}
					b.WriteString(raw[end:])
					remove(b.String())
				}, Children: []core.Node{nswidgets.Icon("x", 12, nil)}})
			}
			chip := core.View{TestID: "reference-" + key, Role: core.RoleButton, LabelFunc: func() string { return mentionLabel(get(), v.ReferenceTitle) }, Style: core.Style{Direction: core.Row, Gap: 4, Padding: core.All(4), CornerRadius: 5}, OnPress: func(core.PressEvent) {
				p := get()
				if p.Ref != nil && v.OnOpenReference != nil {
					v.OnOpenReference(*p.Ref)
				}
			}, Children: children}
			return ui.ContextMenu{TestID: "reference-menu-" + key, Trigger: chip, Children: []core.Node{
				ui.MenuItem{Text: tr("Copy"), OnSelect: func() {
					p := get()
					if p.Ref != nil {
						cx.SetClipboard(model.ReferenceText(*p.Ref, p.TitleSnapshot))
					} else {
						cx.SetClipboard(composer.AgentReferenceText(p.Agent, p.TitleSnapshot))
					}
				}},
				core.Show{When: func() bool { return get().Ref != nil }, Then: func(cx *core.Scope) core.Node {
					return ui.MenuItem{Text: tr("Copy session link"), OnSelect: func() {
						if ref := get().Ref; ref != nil {
							cx.SetClipboard(ref.URL())
						}
					}}
				}},
			}}
		})}}
}
func referenceKey(p model.MessagePart) string {
	if p.Ref != nil {
		return p.Ref.URL()
	}
	return model.AgentURL(p.Agent)
}

// ReferenceInput is the native field used for prompt editing. The main composer
// uses ReferenceEditor; both serialize the same durable reference identities.
type ReferenceInput struct {
	Input          core.Input
	ReferenceTitle func(model.ThreadRef) string
}

func (r ReferenceInput) Render(cx *core.Scope) core.Node {
	in := r.Input
	get, changed := in.Value, in.OnChange
	if get == nil {
		get = func() string { return "" }
	}
	in.Value = func() string { return referenceText(get(), r.ReferenceTitle) }
	in.OnChange = func(value string) {
		if changed != nil {
			changed(referenceEdit(get(), value, r.ReferenceTitle))
		}
	}
	focused := core.NewSignal(cx, false)
	var selected core.TextRange
	in.OnSelectionChange = func(selection core.TextRange) {
		selected = selection
		if r.Input.OnSelectionChange != nil {
			r.Input.OnSelectionChange(selection)
		}
	}
	in.OnFocus = func() {
		focused.Set(true)
		if r.Input.OnFocus != nil {
			r.Input.OnFocus()
		}
	}
	in.OnBlur = func() {
		focused.Set(false)
		if r.Input.OnBlur != nil {
			r.Input.OnBlur()
		}
	}
	editable := func() bool {
		return changed != nil && !in.Readonly && (in.ReadonlyFunc == nil || !in.ReadonlyFunc()) &&
			(in.Disabled == nil || !in.Disabled())
	}
	selectionRange := func() (int, int) {
		visible := referenceText(get(), r.ReferenceTitle)
		a := utf16ByteOffset(visible, max(0, min(selected.Start, selected.End)))
		b := utf16ByteOffset(visible, max(selected.Start, selected.End))
		return referenceRange(get(), a, b, r.ReferenceTitle)
	}
	copyRange := func(cut bool) {
		a, b := selectionRange()
		if a == b {
			return
		}
		source := get()
		cx.SetClipboard(source[a:b])
		if cut && editable() {
			start := min(selected.Start, selected.End)
			selected = core.TextRange{Start: start, End: start}
			changed(source[:a] + source[b:])
		}
	}
	pasteReference := func() {
		source := get()
		a, b := selectionRange()
		cx.ReadClipboard(func(raw string) {
			if !editable() || get() != source {
				return
			}
			if ref, ok := model.ParseThreadURL(strings.TrimSpace(raw)); ok {
				title := ""
				if r.ReferenceTitle != nil {
					title = r.ReferenceTitle(ref)
				}
				changed(source[:a] + model.ReferenceText(ref, title) + source[b:])
			}
		})
	}
	in.OnEditAction = func(action core.EditAction) {
		switch action {
		case core.EditCopy:
			copyRange(false)
		case core.EditCut:
			copyRange(true)
		default:
			if r.Input.OnEditAction != nil {
				r.Input.OnEditAction(action)
			}
		}
	}
	return core.View{Style: core.Style{MinWidth: core.Pt(0), FlexGrow: 1},
		OnContextMenu: func(e core.ContextMenuEvent) {
			a, b := selectionRange()
			cx.PopupMenu([]core.MenuItem{
				{Label: tr("Copy"), Disabled: a == b, OnSelect: func() { copyRange(false) }},
				{Label: tr("Cut"), Disabled: a == b || !editable(), OnSelect: func() { copyRange(true) }},
				{Label: tr("Paste as conversation reference"), Disabled: !editable(), OnSelect: pasteReference},
			}, e.PagePosition, nil)
		},
		Children: []core.Node{in, core.Show{When: focused.Get, Then: func(cx *core.Scope) core.Node {
			cx.OnKeyWith("mod+c", core.KeyOptions{When: core.KeyAlways}, func() { copyRange(false) })
			cx.OnKeyWith("mod+x", core.KeyOptions{When: core.KeyAlways}, func() { copyRange(true) })
			return nil
		}}}}
}
