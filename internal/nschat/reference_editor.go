package nschat

import (
	re "github.com/nsgo-dev/nsgo/base/richeditor"
	"github.com/nsgo-dev/nsgo/core"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

// ReferenceEditor edits plain wire text with atomic, styled references. The
// session owns selection and history; the owner supplies the durable draft.
// CaptureKeys/OnKey follow chat.Composer's contract, through the session keymap
// so native editing and IME composition retain priority outside completions.
type ReferenceEditor struct {
	Input          core.Input
	ReferenceTitle func(model.ThreadRef) string
	Commands       func() []model.ComposerChoice
	Files          func() []string
	CaptureKeys    func() []string
	OnKey          func(string)
	// OnShortcut handles application keys the rich editor would otherwise consume.
	OnShortcut         func(string) bool
	OnEdit             func(string, int)
	OnMove             func()
	OnInvalidReference func()
	Owner              func() string
	Ref                *re.Ref
}

func (r ReferenceEditor) Render(cx *core.Scope) core.Node {
	in := r.Input
	commands := func() []model.ComposerChoice {
		if r.Commands != nil {
			return r.Commands()
		}
		return nil
	}
	files := func() []string {
		if r.Files != nil {
			return r.Files()
		}
		return nil
	}
	tokens := func(raw string) []inlineToken { return editorTokens(raw, r.ReferenceTitle, commands(), files()) }
	exts := []re.Extension{
		{Name: "document", Nodes: []re.NodeSpec{{Name: "doc", Content: "paragraph"}}},
		{Name: "paragraph", Nodes: []re.NodeSpec{{Name: "paragraph", Content: "inline*", Group: "block", Code: true}}},
		{Name: "text", Nodes: []re.NodeSpec{{Name: "text", Group: "inline"}}},
		re.InlineAtom("reference"), re.InlineAtom("file"), re.InlineAtom("command"),
	}
	s, err := re.NewSession(re.SessionConfig{Extensions: exts, Plugins: []*re.Plugin{{Key: "miya-atoms", AppendTransaction: func(_ []*re.Transaction, _ *re.State, st *re.State) *re.Transaction {
		return atomize(st, r.ReferenceTitle, commands(), files())
	}}, re.History(re.HistoryOptions{})}})
	if err != nil {
		panic(err)
	}
	raw := ""
	owner := ""
	syncing := false
	initialized := false
	resetOnSync := true
	focused := core.NewSignal(cx, false)
	makeDoc := func(value string) *re.Node {
		return s.State().Schema.TopNode.Create(nil, re.FragmentFrom(s.State().Schema.Nodes["paragraph"].Create(nil, re.FragmentFrom(editorNodes(s.State().Schema, value, tokens(value))...), nil)), nil)
	}
	set := func(value string, caret int, reset bool) {
		syncing = true
		doc := makeDoc(value)
		p := editorPos(value, tokens(value), caret)
		if reset {
			st, e := re.NewState(re.StateConfig{Schema: s.State().Schema, Doc: doc, Selection: re.TextSelectionAt(doc, p, p), Plugins: s.State().Plugins})
			if e != nil {
				panic(e)
			}
			s.UpdateState(st)
		} else {
			tr := s.State().Tr().ReplaceWith(0, s.State().Doc.Content.Size, doc.Content.Content...)
			tr.SetSelection(re.TextSelectionAt(tr.Doc, p, p))
			s.Dispatch(re.CloseHistory(tr))
		}
		raw = value
		syncing = false
	}
	stop := s.Subscribe(func(st *re.State, trs []*re.Transaction) {
		if syncing {
			return
		}
		if s.Text() != raw {
			resetOnSync = false
			raw = s.Text()
			caret := editorOffset(s.State().Doc, s.State().Selection.Head())
			if r.OnEdit != nil {
				r.OnEdit(raw, caret)
			} else if in.OnChange != nil {
				in.OnChange(raw)
			}
		} else if len(trs) > 0 && r.OnMove != nil {
			for _, tr := range trs {
				if tr.SelectionSet() && !tr.DocChanged() {
					r.OnMove()
					break
				}
			}
		}
	})
	cx.Cleanup(stop)
	cx.Effect(func() {
		value := ""
		if in.Value != nil {
			value = in.Value()
		}
		knownCommands := commands()
		knownFiles := files()
		nextOwner := ""
		if r.Owner != nil {
			nextOwner = r.Owner()
		}
		cx.Untrack(func() {
			if !initialized || value != raw || owner != nextOwner {
				caret := len(value)
				if owner == nextOwner {
					caret = composer.ComposerEditEnd(raw, value)
				}
				if owner != nextOwner {
					resetOnSync = true
				}
				set(value, caret, resetOnSync || value == "")
				initialized = true
				owner = nextOwner
			}
			if tr := atomize(s.State(), r.ReferenceTitle, knownCommands, knownFiles); tr != nil {
				s.Dispatch(tr)
			}
		})
		editable := (in.Disabled == nil || !in.Disabled()) && !in.Readonly && (in.ReadonlyFunc == nil || !in.ReadonlyFunc())
		s.SetEditable(editable)
	})
	// A session plugin consumes only the keys requested by the completion popup.
	p := &re.Plugin{Key: "miya-composer-keys", HandleKeyDown: func(_ *re.Session, key string) bool {
		if r.OnShortcut != nil && r.OnShortcut(key) {
			return true
		}
		names := map[string]string{"Enter": "enter", "Tab": "tab", "ArrowUp": "up", "ArrowDown": "down", "Escape": "escape"}
		if r.CaptureKeys != nil && r.OnKey != nil {
			for _, k := range r.CaptureKeys() {
				if names[key] == k {
					r.OnKey(k)
					return true
				}
			}
		}
		if key == "Enter" && cx.Env().Desktop() {
			if in.OnSubmit != nil {
				in.OnSubmit(raw)
			}
			return true
		}
		if key == "Enter" || key == "Shift-Enter" {
			s.TypeText("\n", nil)
			return true
		}
		if key == "Mod-Enter" && in.OnSubmit != nil {
			if in.OnKeyPress != nil {
				in.OnKeyPress("mod+enter")
			}
			return true
		}
		if key == "Mod-z" {
			return s.Run(re.Undo)
		}
		if key == "Mod-Shift-z" {
			return s.Run(re.Redo)
		}
		if key == "Mod-a" {
			return s.Run(re.SelectAll)
		}
		return false
	}}
	st := s.State()
	keyed, err := re.NewState(re.StateConfig{Schema: st.Schema, Doc: st.Doc, Selection: st.Selection, Plugins: append([]*re.Plugin{p}, st.Plugins...)})
	if err != nil {
		panic(err)
	}
	s.UpdateState(keyed)
	styles := func() re.Styles {
		colors := re.Styles{Content: core.Style{FontSize: 13, LineHeight: 20, Color: nswidgets.ColorsOf(cx).Ink()}, Marks: map[string]core.Style{}}
		for _, kind := range []string{"reference", "file", "command"} {
			colors.Marks[kind] = core.Style{Color: nswidgets.ColorsOf(cx).Link(), Background: nswidgets.ColorsOf(cx).Subtle(), CornerRadius: 5, FontWeight: 500}
		}
		return colors
	}
	view := re.View{Editor: s, Label: in.Label, AutoFocus: in.AutoFocus, OnFocus: func() {
		focused.Set(true)
		if in.OnFocus != nil {
			in.OnFocus()
		}
	}, OnBlur: func() {
		focused.Set(false)
		if in.OnBlur != nil {
			in.OnBlur()
		}
	}, TestID: in.TestID, Style: in.Style, StyleFunc: in.StyleFunc, StylesFunc: styles, OnPasteData: in.OnPaste, Ref: r.Ref}
	// Rich editor exposes its native typing sink through Render. Add Miya's
	// native edit actions here until NSGo offers these props on richeditor.View.
	root := view.Render(cx).(core.View)
	root.Disabled = in.Disabled
	// Report the actual rich content height to the card, as a native input does.
	for i, n := range root.Children {
		if scroll, ok := n.(core.Scroll); ok {
			for j, child := range scroll.Children {
				if content, ok := child.(core.View); ok {
					original := content.OnLayout
					content.OnLayout = func(b core.Bounds) {
						if original != nil {
							original(b)
						}
						if in.OnContentSizeChange != nil {
							in.OnContentSizeChange(b.Size)
						}
					}
					scroll.Children[j] = content
				}
			}
			root.Children[i] = scroll
		}
	}
	root.Children = append(root.Children, core.Show{When: func() bool {
		return focused.Get() && (in.Disabled == nil || !in.Disabled()) && (in.Readonly || in.ReadonlyFunc != nil && in.ReadonlyFunc())
	}, Then: func(cx *core.Scope) core.Node {
		cx.OnKeyWith("mod+a", core.KeyOptions{When: core.KeyAlways}, func() { s.Run(re.SelectAll) })
		return nil
	}})

	root.Children = append(root.Children, core.Show{When: func() bool { return in.Value != nil && in.Value() == "" }, Then: func(*core.Scope) core.Node {
		return core.Text{Content: in.Placeholder, ContentFunc: in.PlaceholderFunc, PointerEvents: core.PointerEventsNone, Style: core.Style{Position: core.Absolute, Top: core.Pt(4), Left: core.Pt(4), FontSize: 13, Color: cx.Palette().TextMuted}}
	}})

	for i, n := range root.Children {
		if sink, ok := n.(core.Input); ok {
			sink.CopyText = func(core.InputSelection) string { text, _ := s.Copy(); return text }
			sink = r.adaptEditorSink(cx, &root, sink, s, func() func() bool {
				ownerAtRead, rawAtRead := owner, raw
				return func() bool { return s.Editable() && ownerAtRead == owner && rawAtRead == raw }
			})
			sink.ReadonlyFunc = func() bool { return in.Readonly || in.ReadonlyFunc != nil && in.ReadonlyFunc() }
			sink.Disabled = in.Disabled
			root.Children[i] = sink
		}
	}
	return root
}
