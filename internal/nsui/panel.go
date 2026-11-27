package nsui

import (
	"context"
	"fmt"
	"io"
	phoneproxy "miya/internal/phone/proxy"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base/diff"
	"github.com/nsgo-dev/nsgo/base/editor"
	"github.com/nsgo-dev/nsgo/base/terminal"
	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	"miya/internal/fsutil"
	"miya/internal/nswidgets"
	projectfiles "miya/internal/project/files"
	projectgit "miya/internal/project/git"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
	svcproto "miya/internal/service/protocol"
)

// The right panel, as on MyGo, holds tabs of surfaces beside the
// conversation: browsers and terminals, as many as asked, and one tab each
// of the workspace's files, changes and runtime, chosen from cards
// while none is open. Its state belongs to the engine; reads of the
// workspace run on goroutines of their own and post their results back.
type surfaceKind int

const (
	surfaceBrowser surfaceKind = iota
	surfaceTerminal
	surfaceFiles
	surfaceReview
	surfaceRuntime
)

var surfaceKinds = []surfaceKind{surfaceBrowser, surfaceTerminal, surfaceFiles, surfaceReview, surfaceRuntime}

func (k surfaceKind) label() string {
	return [...]string{tr("Browser"), tr("Terminal"), tr("Files"), tr("Review"), tr("Background tasks")}[k]
}

func (k surfaceKind) key() string {
	return [...]string{"browser", "terminal", "files", "review", "runtime"}[k]
}

func (k surfaceKind) icon() string {
	return [...]string{"globe", "terminal-2", "folder", "file-diff", "stack-2"}[k]
}

func (k surfaceKind) description() string {
	return [...]string{tr("Open a local app or URL"), tr("Open a terminal in this workspace"), tr("Browse and read workspace files"), tr("Review file changes"), tr("View this conversation's tasks, processes, and services")}[k]
}

// single kinds open one tab; another request shows it again.
func (k surfaceKind) single() bool {
	return k == surfaceFiles || k == surfaceReview || k == surfaceRuntime
}

// surface is one tab. Its signals live as long as the panel.
type surface struct {
	id          int
	kind        surfaceKind
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	webCancel   context.CancelFunc
	webProxyURL string
	webTarget   string
	webService  webservice.Service
	runtimeData *core.Signal[*tracker.ConversationRuntime]
	runtimeBusy *core.Signal[bool]
	runtimeErr  *core.Signal[string]
	runtimeRef  model.ThreadRef

	// A browser's typed address, the URL it loads and the page it shows.
	address, loaded, page, webErr *core.Signal[string]
	web                           *core.Ref

	term     *core.Signal[*terminal.Terminal]
	termErr  *core.Signal[string]
	starting *core.Signal[bool]
	exited   *core.Signal[bool]
}

// fileEditor is an open file: its text as edited and as last saved.
type fileEditor struct {
	rel string
	// image is projectfiles.ImageFileKind's: "bitmap", "svg" or "".
	image           string
	data            []byte
	loading, saving *core.Signal[bool]
	preview         *core.Signal[bool]
	err, saveErr    *core.Signal[string]
	text, saved     *core.Signal[string]
}

func (ed *fileEditor) dirty() bool {
	return ed.image != "bitmap" && !ed.loading.Get() && ed.err.Get() == "" && ed.text.Get() != ed.saved.Get()
}

type treeRow struct {
	entry projectfiles.FileEntry
	depth int
}

// diffSides is the file the review shows: its two texts once read, or a
// note in their place.
type diffSides struct {
	path, old, new, note string
	ready                bool
}

type panel struct {
	cx       *core.Scope
	ctx      context.Context
	ref      *core.Signal[model.ThreadRef]
	threadID int
	lastBusy bool
	host     Host
	open     *core.Signal[bool]

	cwd      *core.Signal[string]
	surfaces *core.Signal[[]*surface]
	// active is the shown surface's id; 0 shows the cards.
	active *core.Signal[int]
	nextID int

	// The files: the directories read, by path, and the open editors. The
	// maps change on the engine; tree and editorsRev say they did.
	selected   *core.Signal[string]
	tree       *core.Signal[int]
	editorsRev *core.Signal[int]
	expanded   map[string]bool
	dirs       map[string][]projectfiles.FileEntry
	dirErr     map[string]string
	loading    map[string]bool
	editors    map[string]*fileEditor

	source   *core.Signal[projectgit.DiffSource]
	diff     *core.Signal[*projectgit.DiffSnapshot]
	diffErr  *core.Signal[string]
	diffBusy *core.Signal[bool]
	diffFile *core.Signal[string]
	sides    *core.Signal[diffSides]
	// diffGen drops replies of reads a newer one superseded.
	diffGen int
}

// filePanel keeps each conversation's state for the life of the shell. Switching
// views disposes their bindings, but leaves terminal connections and tab state alive.
func filePanel(cx *core.Scope, c Host, open *core.Signal[bool], onCurrent ...func(*panel)) (head, body core.Node) {
	ctx := cx.Context()
	panels := map[model.ThreadRef]*panel{}
	// A thread has no stable ref until its backend session starts. Keep its
	// provisional ownership even while another conversation is selected.
	pending := map[int]*panel{}
	known := map[int]bool{}
	for _, thread := range c.AllThreads() {
		known[thread.ID] = true
	}
	shown := core.NewSignal[*panel](cx, nil)
	current := Live(c.Current)
	cwd := Live(func() string { return c.State().Cwd })
	var previousID int
	cx.Effect(func() {
		thread, dir := current(), cwd()
		cx.Untrack(func() {
			// A newly allocated sidebar row inherits the unsent draft, before
			// it has a backend session ID. Selecting an existing row never does.
			if thread.ID != 0 && previousID == 0 && !known[thread.ID] && pending[thread.ID] == nil {
				pending[thread.ID] = pending[0]
				delete(pending, 0)
			}
			all := append(slices.Clone(c.AllThreads()), thread)
			for _, t := range all {
				ref := c.ThreadRef(t)
				if p := pending[t.ID]; p != nil && ref.Valid() {
					if panels[ref] == nil {
						panels[ref] = p
					} else {
						p.closeAll()
					}
					delete(pending, t.ID)
					p.ref.Set(ref)
					p.threadID = t.ID
				}
				known[t.ID] = true
			}
			ref := c.ThreadRef(thread)
			var p *panel
			if ref.Valid() {
				p = panels[ref]
			} else {
				ref = model.ThreadRef{}
				p = pending[thread.ID]
			}
			if p == nil {
				p = newPanel(cx, ctx, c, open)
			}
			if ref.Valid() {
				panels[ref] = p
			} else {
				pending[thread.ID] = p
			}
			p.ref.Set(ref)
			p.threadID = thread.ID
			if dir != p.cwd.GetUntracked() {
				p.resetWorkspace(dir)
				p.reload()
			} else if p.lastBusy && !thread.Busy {
				p.workspaceChanged()
			}
			p.lastBusy = thread.Busy
			shown.Set(p)
			for _, f := range onCurrent {
				f(p)
			}
			previousID = thread.ID
		})
	})
	cx.Cleanup(func() {
		for _, p := range panels {
			p.closeAll()
		}
		for _, p := range pending {
			if p != nil {
				p.closeAll()
			}
		}
	})
	// The tabs go in the shell's header row over the panel, level with the
	// conversation's title; the panel's surface fills the rest.
	head = core.Dynamic(func(cx *core.Scope) core.Node {
		if p := shown.Get(); p != nil {
			return p.header(cx)
		}
		return core.Group(nil)
	})
	body = core.Dynamic(func(cx *core.Scope) core.Node {
		if p := shown.Get(); p != nil {
			return p.view(cx)
		}
		return core.Group(nil)
	})
	return head, body
}

func newPanel(cx *core.Scope, ctx context.Context, c Host, open *core.Signal[bool]) *panel {
	p := &panel{cx: cx, host: c, open: open, ctx: ctx, ref: core.NewSignal(cx, model.ThreadRef{}),
		cwd:        core.NewSignal(cx, ""),
		surfaces:   core.NewSignalFunc[[]*surface](cx, nil, slices.Equal),
		active:     core.NewSignal(cx, 0),
		selected:   core.NewSignal(cx, ""),
		tree:       core.NewSignal(cx, 0),
		editorsRev: core.NewSignal(cx, 0),
		source:     core.NewSignal(cx, projectgit.SourceUncommitted),
		diff:       core.NewSignal[*projectgit.DiffSnapshot](cx, nil),
		diffErr:    core.NewSignal(cx, ""),
		diffBusy:   core.NewSignal(cx, false),
		diffFile:   core.NewSignal(cx, ""),
		sides:      core.NewSignal(cx, diffSides{}),
	}
	p.resetWorkspace("")
	return p
}

func (p *panel) view(cx *core.Scope) core.Node {
	active := core.NewMemo(cx, func() *surface {
		id := p.active.Get()
		for _, s := range p.surfaces.Get() {
			if s.id == id {
				return s
			}
		}
		return nil
	})
	return core.View{TestID: "file-panel", Style: ui.FillRest.Merge(core.Style{Width: core.Pct(100)}), Children: []core.Node{
		core.Dynamic(func(cx *core.Scope) core.Node {
			s := active.Get()
			if s == nil {
				return p.chooser(cx)
			}
			return core.View{Style: ui.FillRest, Children: []core.Node{p.body(cx, s)}}
		}),
	}}
}

func (p *panel) find(k surfaceKind) *surface {
	for _, s := range p.surfaces.GetUntracked() {
		if s.kind == k {
			return s
		}
	}
	return nil
}

func bump(s *core.Signal[int]) { s.Update(func(n int) int { return n + 1 }) }

// resetWorkspace forgets what the files and the review read of the old
// workspace; terminals and browsers keep running.
func (p *panel) resetWorkspace(cwd string) {
	p.cwd.Set(cwd)
	p.selected.Set("")
	p.expanded = map[string]bool{}
	p.dirs = map[string][]projectfiles.FileEntry{}
	p.dirErr = map[string]string{}
	p.loading = map[string]bool{}
	p.editors = map[string]*fileEditor{}
	bump(p.tree)
	bump(p.editorsRev)
	p.diffGen++
	p.diff.Set(nil)
	p.diffErr.Set("")
	p.diffBusy.Set(false)
	p.diffFile.Set("")
	p.sides.Set(diffSides{})
}

// reload reads the open files and review of a new workspace.
func (p *panel) reload() {
	if p.find(surfaceFiles) != nil {
		p.loadDir("")
	}
	if p.find(surfaceReview) != nil {
		p.refreshDiff()
	}
}

func (p *panel) workspaceChanged() {
	if p.find(surfaceReview) != nil {
		p.refreshDiff()
	}
	for rel := range p.dirs {
		p.loadDir(rel)
	}
}

func (p *panel) openSurface(k surfaceKind) {
	p.open.Set(true)
	if k.single() {
		if s := p.find(k); s != nil {
			p.active.Set(s.id)
			if k == surfaceReview {
				p.refreshDiff()
			}
			return
		}
	}
	p.nextID++
	cx := p.cx
	ctx, cancel := context.WithCancel(p.ctx)
	s := &surface{id: p.nextID, kind: k, ctx: ctx, cancel: cancel,
		runtimeData: core.NewSignal[*tracker.ConversationRuntime](cx, nil), runtimeBusy: core.NewSignal(cx, false), runtimeErr: core.NewSignal(cx, ""),
		address: core.NewSignal(cx, ""), loaded: core.NewSignal(cx, ""), page: core.NewSignal(cx, ""), webErr: core.NewSignal(cx, ""), web: &core.Ref{},
		term: core.NewSignal[*terminal.Terminal](cx, nil), termErr: core.NewSignal(cx, ""), starting: core.NewSignal(cx, false), exited: core.NewSignal(cx, false)}
	p.surfaces.Set(append(slices.Clone(p.surfaces.GetUntracked()), s))
	p.active.Set(s.id)
	switch k {
	case surfaceTerminal:
		p.startTerminal(s)
	case surfaceFiles:
		p.loadDir("")
	case surfaceReview:
		p.refreshDiff()
	}
}

func (p *panel) closeSurface(id int) {
	list := slices.Clone(p.surfaces.GetUntracked())
	i := slices.IndexFunc(list, func(s *surface) bool { return s.id == id })
	if i < 0 {
		return
	}
	closeSurface(list[i])
	list = slices.Delete(list, i, i+1)
	p.surfaces.Set(list)
	if p.active.GetUntracked() != id {
		return
	}
	switch {
	case len(list) == 0:
		p.active.Set(0)
	case i < len(list):
		p.active.Set(list[i].id)
	default:
		p.active.Set(list[len(list)-1].id)
	}
}

func closeSurface(s *surface) {
	s.closed = true
	s.cancel()
}

func (p *panel) closeAll() {
	for _, s := range p.surfaces.GetUntracked() {
		closeSurface(s)
	}
}

func (p *panel) surfaceLabel(s *surface) string {
	switch s.kind {
	case surfaceFiles:
		if rel := p.selected.Get(); rel != "" {
			return path.Base(rel)
		}
	case surfaceBrowser:
		if u, err := url.Parse(s.page.Get()); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return s.kind.label()
}

func (p *panel) surfaceDirty(s *surface) bool {
	if s.kind != surfaceFiles {
		return false
	}
	p.editorsRev.Get()
	for _, ed := range p.editors {
		if ed.dirty() {
			return true
		}
	}
	return false
}

func (p *panel) header(cx *core.Scope) core.Node {
	add := make([]core.Node, 0, len(surfaceKinds))
	for _, k := range surfaceKinds {
		add = append(add, ui.MenuItem{Text: k.label(), Icon: nswidgets.IconOf(k.icon()), TestID: "panel-add-" + k.key(),
			Checked:  func() bool { p.surfaces.Get(); return k.single() && p.find(k) != nil },
			OnSelect: func() { p.openSurface(k) }})
	}
	return core.View{Label: tr("Right panel tabs"), Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pct(100)}, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
			core.Each(p.surfaces.Get, func(s *surface) int { return s.id }, func(cx *core.Scope, get func() *surface, _ func() int) core.Node {
				return p.tab(cx, core.Untracked(cx, get))
			}),
		}},
		core.Show{When: func() bool { return len(p.surfaces.Get()) > 0 }, Then: func(cx *core.Scope) core.Node {
			return ui.Menu{TestID: "panel-add", Label: tr("Add panel tab"), Trigger: ui.Button{Icon: nswidgets.IconOf("plus"), Label: tr("Add panel tab"), Variant: ui.ButtonGhost, Size: ui.SizeSmall}, Children: add}
		}},
	}}
}

func (p *panel) tab(cx *core.Scope, s *surface) core.Node {
	colors := nswidgets.ColorsOf(cx)
	id := strconv.Itoa(s.id)
	active := func() bool { return p.active.Get() == s.id }
	label := func() string { return p.surfaceLabel(s) }
	return core.View{TestID: "panel-tab-" + id, Role: core.RoleButton, LabelFunc: label, OnPress: func(core.PressEvent) { p.active.Set(s.id) },
		StyleFunc: func() core.Style {
			st := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Height: core.Pt(28), MinWidth: core.Pt(72), MaxWidth: core.Pt(176), FlexShrink: 1, Padding: core.Symmetric(0, 8), CornerRadius: 6}
			if active() {
				st.Background = colors.Chosen()
			}
			return st
		},
		Children: []core.Node{
			nswidgets.Icon(s.kind.icon(), 13, colors.Muted),
			core.Text{TestID: "panel-tab-label-" + id, ContentFunc: label, MaxLines: 1, StyleFunc: func() core.Style {
				ink := colors.Muted()
				if active() {
					ink = colors.Ink()
				}
				return core.Style{FontSize: 12, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Color: ink}
			}},
			core.Show{When: func() bool { return p.surfaceDirty(s) }, Then: func(cx *core.Scope) core.Node {
				return core.View{TestID: "panel-dirty-" + id, Label: tr("Unsaved changes. Press ⌘S to save."), StyleFunc: func() core.Style {
					return core.Style{Width: core.Pt(7), Height: core.Pt(7), CornerRadius: 4, FlexShrink: 0, Background: colors.WarnInk()}
				}}
			}},
			ui.IconButton{TestID: "panel-close-" + id, Icon: nswidgets.IconOf("x"), LabelFunc: func() string { return tr("Close") + label() },
				Variant: ui.IconButtonGhost, Size: 16, IconSize: 10, OnPress: func() { p.closeSurface(s.id) }},
		},
	}
}

// chooser offers a card for each kind while no tab is shown.
func (p *panel) chooser(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	card := func(k surfaceKind) core.Node {
		return core.View{TestID: "panel-open-" + k.key(), Role: core.RoleButton, Label: k.label(), OnPress: func(core.PressEvent) { p.openSurface(k) },
			StyleFunc: func() core.Style {
				return core.Style{Height: core.Pt(112), FlexGrow: 1, FlexShrink: 1, Width: core.Pt(0), MinWidth: core.Pt(0), Padding: core.All(14), Gap: 4,
					CornerRadius: 8, BorderWidth: 1, BorderColor: colors.Line(), Background: colors.Panel()}
			},
			Children: []core.Node{
				nswidgets.Icon(k.icon(), 18, colors.Muted),
				core.Text{Content: k.label(), Style: core.Style{FontSize: 12, FontWeight: ui.Semibold, Margin: core.Edges{Top: 8}}},
				core.Text{Content: k.description(), MaxLines: 2, StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: colors.Muted()} }},
			}}
	}
	row := func(kinds ...surfaceKind) core.Node {
		cards := make([]core.Node, 0, len(kinds))
		for _, k := range kinds {
			cards = append(cards, card(k))
		}
		return core.View{Style: core.Style{Direction: core.Row, Gap: 8, Width: core.Pct(100)}, Children: cards}
	}
	return core.View{TestID: "panel-chooser", Style: ui.FillRest.Merge(core.Style{AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.Edges{Left: 20, Right: 20, Bottom: 32}}), Children: []core.Node{
		core.View{Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(420), AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
			core.Text{Content: tr("Open a panel"), Style: core.Style{FontSize: 13, FontWeight: ui.Semibold}},
			nswidgets.MutedText(cx, func() string { return tr("Choose what to show in the right panel") }),
			core.View{Style: core.Style{Height: core.Pt(10)}},
			row(surfaceBrowser, surfaceTerminal),
			row(surfaceFiles, surfaceReview),
			row(surfaceRuntime),
		}},
	}}
}

func (p *panel) body(cx *core.Scope, s *surface) core.Node {
	switch s.kind {
	case surfaceBrowser:
		return p.browserView(cx, s)
	case surfaceTerminal:
		return p.terminalView(cx, s)
	case surfaceFiles:
		return p.filesView(cx)
	case surfaceRuntime:
		return p.runtimeView(cx, s)
	default:
		return p.reviewView(cx)
	}
}

func panelMessage(cx *core.Scope, title, detail string, extra ...core.Node) core.Node {
	children := []core.Node{core.Text{Content: title, Selectable: core.SelectableOn, Style: core.Style{FontSize: 13, FontWeight: ui.Semibold}}}
	if detail != "" {
		children = append(children, core.Text{Content: detail, Selectable: core.SelectableOn, StyleFunc: func() core.Style {
			return core.Style{FontSize: 12, Color: nswidgets.ColorsOf(cx).Muted(), MaxWidth: core.Pct(100)}
		}})
	}
	return core.View{TestID: "panel-message", Style: ui.FillRest.Merge(core.Style{AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 6, Padding: core.All(24)}),
		Children: append(children, extra...)}
}

func panelLoading(cx *core.Scope, text string) core.Node {
	return core.View{Style: ui.FillRest.Merge(core.Style{Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 8}), Children: []core.Node{
		ui.Spinner{Size: 16},
		nswidgets.MutedText(cx, func() string { return text }),
	}}
}

// panelBar is the toolbar under the tabs, ruled off from what it controls.
func panelBar(cx *core.Scope, children ...core.Node) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{Style: core.Style{FlexShrink: 0}, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Height: core.Pt(38), Padding: core.Symmetric(0, 10)}, Children: children},
		core.View{StyleFunc: func() core.Style { return core.Style{Height: core.Pt(1), Background: colors.Line()} }},
	}}
}

func panelSmallText(cx *core.Scope, content func() string, tint func(nswidgets.Colors) core.Color) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.Text{ContentFunc: content, MaxLines: 1, StyleFunc: func() core.Style { return core.Style{FontSize: 11, FlexShrink: 0, Color: tint(colors)} }}
}

func muted(c nswidgets.Colors) core.Color   { return c.Muted() }
func success(c nswidgets.Colors) core.Color { return c.Success() }
func danger(c nswidgets.Colors) core.Color  { return c.Danger() }

// browserView shows public pages directly and remote local services through
// the service proxy, keeping the user's address separate from the proxy URL.
func (p *panel) browserView(cx *core.Scope, s *surface) core.Node {
	id := strconv.Itoa(s.id)
	return core.View{Style: ui.FillRest, Children: []core.Node{
		panelBar(cx,
			core.Show{When: func() bool { return s.loaded.Get() != "" }, Then: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{Direction: core.Row, Gap: 2}, Children: []core.Node{
					nswidgets.ToolButton("arrow-left", tr("Back"), s.web.GoBack),
					nswidgets.ToolButton("arrow-right", tr("Forward"), s.web.GoForward),
					nswidgets.ToolButton("refresh", tr("Reload"), s.web.Reload),
				}}
			}},
			ui.Input{TestID: "browser-address-" + id, Label: tr("Address"), Placeholder: tr("Enter a URL or localhost port"),
				Value: s.address.Get, OnChange: s.address.Set, OnSubmit: func(string) { p.navigate(s) },
				AutoFocus: s.loaded.GetUntracked() == "", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
		),
		core.Dynamic(func(cx *core.Scope) core.Node {
			if err := s.webErr.Get(); err != "" {
				return panelMessage(cx, tr("Browser unavailable"), err)
			}
			if s.loaded.Get() == "" {
				return core.Group(nil)
			}
			return core.WebView{TestID: "browser-" + id, Label: tr("Browser"), Ref: s.web, Style: ui.FillRest,
				SourceFunc: func() core.WebSource { return core.WebSrc(s.loaded.Get()) },
				OnNavigationStateChange: func(n core.WebNavigation) {
					target := browserPageURL(s, n.URL)
					s.page.Set(target)
					s.address.Set(target)
				},
				OnError: func(err error) { s.webErr.Set(err.Error()) }}
		}),
	}}
}

func (p *panel) navigate(s *surface) {
	target := browserURL(s.address.GetUntracked())
	if target == "" {
		return
	}
	s.address.Set(target)
	if p.host.RemoteHost() {
		p.navigateRemoteBrowser(s, target, webservice.Service{})
		return
	}
	s.webErr.Set("")
	if s.loaded.GetUntracked() == target {
		s.web.Reload()
		return
	}
	s.loaded.Set(target)
	s.page.Set(target)
}

// browserURL turns what is typed in the address bar into a URL: a port is a
// local server, a host without a scheme is https, local hosts http.
func browserURL(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "http://localhost:" + s
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "about:") {
		return s
	}
	for _, local := range []string{"localhost", "127.", "0.0.0.0", "[::1]"} {
		if strings.HasPrefix(s, local) {
			return "http://" + s
		}
	}
	return "https://" + s
}

func (p *panel) startTerminal(s *surface) {
	s.starting.Set(true)
	s.termErr.Set("")
	s.exited.Set(false)
	dir, files := p.cwd.GetUntracked(), p.host.WorkspaceFiles()
	go func() {
		opts := terminal.Options{Dir: dir}
		var err error
		// A mirrored window's files give a connection to the host's shell;
		// a local one's none, and the terminal runs the user's shell.
		opts.Conn, err = files.Terminal(dir)
		var t *terminal.Terminal
		if err == nil {
			t, err = terminal.New(opts)
		}
		if err != nil && opts.Conn != nil {
			opts.Conn.Close()
		}
		if t != nil {
			context.AfterFunc(s.ctx, func() { _ = t.Close() })
		}
		p.cx.Post(func() {
			if s.closed {
				return
			}
			s.starting.Set(false)
			if err != nil {
				s.termErr.Set(err.Error())
				return
			}
			s.term.Set(t)
			go func() {
				<-t.Done()
				p.cx.Post(func() {
					if s.term.GetUntracked() == t {
						s.exited.Set(true)
					}
				})
			}()
		})
	}()
}

func (p *panel) restartTerminal(s *surface) {
	if t := s.term.GetUntracked(); t != nil {
		go t.Close()
		s.term.Set(nil)
	}
	p.startTerminal(s)
}

func (p *panel) terminalView(cx *core.Scope, s *surface) core.Node {
	id := strconv.Itoa(s.id)
	restart := func(cx *core.Scope, title, detail string) core.Node {
		return panelMessage(cx, title, detail, ui.Button{TestID: "terminal-restart-" + id, Text: tr("Restart"), Variant: ui.ButtonSecondary, OnPress: func() { p.restartTerminal(s) }})
	}
	colors := nswidgets.ColorsOf(cx)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if s.starting.Get() {
			return panelLoading(cx, tr("Starting terminal…"))
		}
		if err := s.termErr.Get(); err != "" {
			return restart(cx, tr("Terminal unavailable"), err)
		}
		t := s.term.Get()
		switch {
		case t == nil:
			return restart(cx, tr("Terminal unavailable"), tr("Close and reopen the terminal panel"))
		case s.exited.Get():
			return restart(cx, tr("Terminal exited"), fmt.Sprintf(tr("Exit code %d"), t.ExitCode()))
		}
		return core.View{Style: ui.FillRest, StyleFunc: func() core.Style {
			return ui.FillRest.Merge(core.Style{Background: colors.Canvas(), Padding: core.Edges{Top: 6, Right: 4, Bottom: 4, Left: 8}})
		}, Children: []core.Node{terminal.View{TestID: "terminal-" + id, Label: tr("Terminal"), Terminal: t, AutoFocus: true, Style: ui.FillRest}}}
	})
}

func (p *panel) filesView(cx *core.Scope) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if rel := p.selected.Get(); rel != "" {
			if ed := p.editors[rel]; ed != nil {
				return p.editorView(cx, ed)
			}
		}
		return p.treeView(cx)
	})
}

func (p *panel) treeRows() []treeRow {
	var rows []treeRow
	var visit func(rel string, depth int)
	visit = func(rel string, depth int) {
		for _, e := range p.dirs[rel] {
			rows = append(rows, treeRow{entry: e, depth: depth})
			if e.Dir && p.expanded[e.Rel] {
				visit(e.Rel, depth+1)
			}
		}
	}
	visit("", 0)
	return rows
}

func (p *panel) treeView(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	// status is what shows in place of the rows, "" for the rows.
	status := core.NewMemo(cx, func() string {
		p.tree.Get()
		switch {
		case p.dirErr[""] != "":
			return "error"
		case len(p.dirs[""]) > 0:
			return ""
		case p.loading[""]:
			return "loading"
		default:
			return "empty"
		}
	})
	rows := func() []treeRow { p.tree.Get(); return p.treeRows() }
	return core.Dynamic(func(cx *core.Scope) core.Node {
		switch status.Get() {
		case "error":
			return panelMessage(cx, tr("Unable to read workspace"), p.dirErr[""])
		case "loading":
			return panelLoading(cx, tr("Loading files…"))
		case "empty":
			return panelMessage(cx, tr("This folder is empty"), p.cwd.GetUntracked())
		}
		return core.Scroll{TestID: "file-tree", Style: ui.FillRest, Children: []core.Node{
			core.View{Style: core.Style{Padding: core.Symmetric(4, 8)}, Children: []core.Node{
				core.Each(rows, func(r treeRow) string { return r.entry.Rel }, func(cx *core.Scope, get func() treeRow, _ func() int) core.Node {
					r := core.Untracked(cx, get)
					e := r.entry
					var lead []core.Node
					if e.Dir {
						lead = []core.Node{
							core.Dynamic(func(cx *core.Scope) core.Node {
								p.tree.Get()
								if p.expanded[e.Rel] {
									return nswidgets.Icon("chevron-down", 12, colors.Muted)
								}
								return nswidgets.Icon("chevron-right", 12, colors.Muted)
							}),
							nswidgets.Icon("folder", 14, colors.Faint),
						}
					} else {
						lead = []core.Node{core.View{Style: core.Style{Width: core.Pt(12), FlexShrink: 0}}, nswidgets.Icon("file", 14, colors.Muted)}
					}
					children := append(lead,
						core.Text{Content: e.Name, MaxLines: 1, Style: core.Style{FontSize: 12, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
						core.Show{When: func() bool { p.tree.Get(); return e.Dir && p.loading[e.Rel] }, Then: func(cx *core.Scope) core.Node { return ui.Spinner{Size: 11} }},
					)
					return core.View{TestID: "file-" + e.Rel, Role: core.RoleButton, Label: e.Name,
						OnPress: func(core.PressEvent) { p.pressEntry(e) },
						Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Height: core.Pt(26), CornerRadius: 6,
							Padding: core.Edges{Left: core.Points(6 + 14*r.depth), Right: 6}},
						Children: children}
				}),
			}},
		}}
	})
}

func (p *panel) pressEntry(e projectfiles.FileEntry) {
	if !e.Dir {
		p.openFile(e.Rel)
		return
	}
	p.expanded[e.Rel] = !p.expanded[e.Rel]
	bump(p.tree)
	if p.expanded[e.Rel] {
		p.loadDir(e.Rel)
	}
}

func (p *panel) loadDir(rel string) {
	if p.loading[rel] {
		return
	}
	p.loading[rel] = true
	bump(p.tree)
	root, files := p.cwd.GetUntracked(), p.host.WorkspaceFiles()
	go func() {
		entries, err := files.ReadDir(root, rel)
		p.cx.Post(func() {
			if p.cwd.GetUntracked() != root {
				return
			}
			delete(p.loading, rel)
			if err != nil {
				p.dirErr[rel] = err.Error()
				delete(p.dirs, rel)
			} else {
				delete(p.dirErr, rel)
				p.dirs[rel] = entries
			}
			bump(p.tree)
		})
	}()
}

func markdownFile(rel string) bool { return strings.EqualFold(path.Ext(rel), ".md") }

// openFile shows rel, reading it unless it is open already: an open editor
// keeps its unsaved edits.
func (p *panel) openFile(rel string) {
	if ed := p.editors[rel]; ed != nil && ed.err.GetUntracked() == "" {
		p.selected.Set(rel)
		return
	}
	cx := p.cx
	kind := projectfiles.ImageFileKind(rel)
	ed := &fileEditor{rel: rel, image: kind,
		loading: core.NewSignal(cx, true), saving: core.NewSignal(cx, false), preview: core.NewSignal(cx, markdownFile(rel) || kind != ""),
		err: core.NewSignal(cx, ""), saveErr: core.NewSignal(cx, ""), text: core.NewSignal(cx, ""), saved: core.NewSignal(cx, "")}
	p.editors[rel] = ed
	bump(p.editorsRev)
	p.selected.Set(rel)
	root, files := p.cwd.GetUntracked(), p.host.WorkspaceFiles()
	go func() {
		var text string
		var data []byte
		var err error
		if kind == "bitmap" {
			data, err = files.ReadImage(root, rel)
		} else {
			text, err = files.ReadText(root, rel)
		}
		p.cx.Post(func() {
			if p.cwd.GetUntracked() != root || p.editors[rel] != ed {
				return
			}
			if err != nil {
				ed.err.Set(err.Error())
			} else {
				ed.data = data
				ed.text.Set(text)
				ed.saved.Set(text)
			}
			ed.loading.Set(false)
		})
	}()
}

func (p *panel) saveFile(ed *fileEditor) {
	if ed.saving.GetUntracked() || core.Untracked(p.cx, ed.dirty) == false {
		return
	}
	text, root, rel := ed.text.GetUntracked(), p.cwd.GetUntracked(), ed.rel
	remote, cl := p.host.RemoteHost(), p.host.State().Remote
	ed.saving.Set(true)
	ed.saveErr.Set("")
	go func() {
		var err error
		switch {
		case remote && cl == nil:
			err = svcproto.ErrClosed
		case remote:
			err = cl.Call("host", svcproto.HostRequest{Action: "write", Project: root, Path: rel, Text: text}, nil)
		default:
			err = fsutil.WriteFileAtomic(filepath.Join(root, rel), func(f *os.File) error {
				_, err := io.WriteString(f, text)
				return err
			})
		}
		p.cx.Post(func() {
			ed.saving.Set(false)
			if p.cwd.GetUntracked() != root {
				return
			}
			if err != nil {
				ed.saveErr.Set(tr("Unable to save: ") + err.Error())
				return
			}
			ed.saved.Set(text)
			if p.find(surfaceReview) != nil {
				p.refreshDiff()
			}
		})
	}()
}

// editorView edits an open file, or previews a Markdown or HTML file or an
// image.
func (p *panel) editorView(cx *core.Scope, ed *fileEditor) core.Node {
	colors := nswidgets.ColorsOf(cx)
	rel := ed.rel
	previewable := markdownFile(rel) || htmlFile(rel) || ed.image == "svg"
	cx.OnKey("mod+s", func() { p.saveFile(ed) })
	return core.View{Style: ui.FillRest, Children: []core.Node{
		panelBar(cx,
			nswidgets.ToolButton("arrow-left", tr("Back to file list"), func() { p.selected.Set("") }),
			core.Text{Content: rel, MaxLines: 1, StyleFunc: func() core.Style {
				return core.Style{FontSize: 12, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Color: colors.Muted()}
			}},
			core.Show{When: func() bool { return previewable && !ed.loading.Get() && ed.err.Get() == "" }, Then: func(cx *core.Scope) core.Node {
				return ui.IconButton{TestID: "file-preview", Size: 32, IconSize: 17,
					Icon: nswidgets.IconOf("eye"),
					LabelFunc: func() string {
						if ed.preview.Get() {
							return tr("Source")
						}
						return tr("Preview")
					},
					OnPress: func() { ed.preview.Update(func(b bool) bool { return !b }) }}
			}},
			core.Show{When: ed.dirty, Then: func(cx *core.Scope) core.Node {
				return ui.IconButton{TestID: "file-save", Icon: nswidgets.IconOf("device-floppy"), Label: tr("Save (⌘S)"), Size: 32, IconSize: 17,
					Disabled: ed.saving.Get, OnPress: func() { p.saveFile(ed) }}
			}},
		),
		core.Show{When: func() bool { return ed.saveErr.Get() != "" }, Then: func(cx *core.Scope) core.Node {
			return core.Text{TestID: "file-save-error", ContentFunc: ed.saveErr.Get, Selectable: core.SelectableOn, StyleFunc: func() core.Style {
				return core.Style{FontSize: 11, Color: colors.Danger(), Padding: core.Symmetric(4, 10)}
			}}
		}},
		core.Show{When: ed.saving.Get, Then: func(cx *core.Scope) core.Node {
			return core.Text{Content: tr("Saving…"), StyleFunc: func() core.Style {
				return core.Style{FontSize: 11, Color: colors.Muted(), Padding: core.Symmetric(4, 10)}
			}}
		}},
		core.Dynamic(func(cx *core.Scope) core.Node {
			switch {
			case ed.loading.Get():
				return panelLoading(cx, tr("Loading files…"))
			case ed.err.Get() != "":
				return panelMessage(cx, tr("Unable to open this file"), ed.err.Get())
			case ed.preview.Get() && ed.image != "":
				return p.imagePreview(cx, ed)
			case ed.preview.Get() && htmlFile(rel):
				return p.htmlPreview(cx, ed)
			case ed.preview.Get() && markdownFile(rel):
				return core.Scroll{TestID: "file-markdown", StyleFunc: func() core.Style { return ui.FillRest.Merge(core.Style{Background: colors.Canvas()}) }, Children: []core.Node{
					core.View{Style: core.Style{Padding: core.Symmetric(16, 20)}, Children: []core.Node{
						ui.TextView{MarkdownFunc: ed.text.Get, Selectable: true, OnLinkPress: func(e textview.LinkPressEvent) { cx.OpenURL(e.URL, nil) }},
					}},
				}}
			}
			return editor.Editor{TestID: "file-editor", Label: rel, DefaultValue: ed.text.GetUntracked(), OnChange: ed.text.Set, Language: rel,
				StyleFunc: func() core.Style {
					return ui.FillRest.Merge(core.Style{Width: core.Pct(100), Background: colors.Canvas()})
				}}
		}),
	}}
}

func (p *panel) imagePreview(cx *core.Scope, ed *fileEditor) core.Node {
	colors := nswidgets.ColorsOf(cx)
	data := ed.data
	if ed.image == "svg" {
		data = []byte(ed.text.GetUntracked())
	}
	failed := core.NewSignal(cx, false)
	size := core.NewSignal(cx, "")
	return core.View{TestID: "file-image", StyleFunc: func() core.Style {
		return ui.FillRest.Merge(core.Style{Padding: core.All(16), Gap: 8, Background: colors.Canvas(), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter})
	}, Children: []core.Node{
		core.Show{When: failed.Get, Then: func(cx *core.Scope) core.Node {
			return panelMessage(cx, tr("Unable to preview this image"), tr("Invalid SVG content or missing dimensions. Switch to source to view it."))
		}, Else: func(cx *core.Scope) core.Node {
			return core.View{Style: ui.FillRest.Merge(core.Style{Width: core.Pct(100), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 8}), Children: []core.Node{
				core.Image{Source: core.ImageSource{Data: data}, Label: path.Base(ed.rel), ResizeMode: core.Contain,
					Style:   ui.FillRest.Merge(core.Style{Width: core.Pct(100)}),
					OnLoad:  func(px core.Size) { size.Set(fmt.Sprintf("%d × %d", int(px.Width+0.5), int(px.Height+0.5))) },
					OnError: func(error) { failed.Set(true) }},
				panelSmallText(cx, size.Get, muted),
			}}
		}},
	}}
}

func (p *panel) context() context.Context {
	if ctx := p.host.State().Ctx; ctx != nil {
		return ctx
	}
	return context.Background()
}

func (p *panel) refreshDiff() {
	p.diffGen++
	gen, root, src := p.diffGen, p.cwd.GetUntracked(), p.source.GetUntracked()
	previous := p.diffFile.GetUntracked()
	p.diffBusy.Set(true)
	parent, files := p.context(), p.host.WorkspaceFiles()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		snap, err := files.Diff(ctx, root, src)
		p.cx.Post(func() {
			if p.diffGen != gen || p.cwd.GetUntracked() != root {
				return
			}
			p.diffBusy.Set(false)
			if err != nil {
				if p.diff.GetUntracked() == nil {
					p.diffErr.Set(err.Error())
				} else {
					p.diffErr.Set(tr("Refresh failed: ") + err.Error())
				}
				return
			}
			p.diffErr.Set("")
			p.diff.Set(&snap)
			pick := 0
			for i, f := range snap.Files {
				if f.Path == previous {
					pick = i
				}
			}
			p.diffFile.Set("")
			p.sides.Set(diffSides{})
			p.loadDiffFile(pick)
		})
	}()
}

func (p *panel) loadDiffFile(i int) {
	snap := p.diff.GetUntracked()
	if snap == nil || i < 0 || i >= len(snap.Files) {
		return
	}
	f, gen, root := snap.Files[i], p.diffGen, p.cwd.GetUntracked()
	p.diffFile.Set(f.Path)
	if f.Binary {
		p.sides.Set(diffSides{path: f.Path, note: tr("Binary file; no diff available")})
		return
	}
	p.sides.Set(diffSides{path: f.Path})
	parent, files, shot := p.context(), p.host.WorkspaceFiles(), *snap
	go func() {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		old, new, err := files.Sides(ctx, root, shot, f)
		p.cx.Post(func() {
			if p.diffGen != gen || p.diffFile.GetUntracked() != f.Path {
				return
			}
			if err != nil {
				p.sides.Set(diffSides{path: f.Path, note: err.Error()})
				return
			}
			p.sides.Set(diffSides{path: f.Path, old: old, new: new, ready: true})
		})
	}()
}

func (p *panel) chooseSource(src projectgit.DiffSource) {
	if src == p.source.GetUntracked() {
		return
	}
	p.source.Set(src)
	p.diff.Set(nil)
	p.diffErr.Set("")
	p.diffFile.Set("")
	p.sides.Set(diffSides{})
	p.refreshDiff()
}

func (p *panel) reviewView(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	names := projectgit.DiffSourceNames()
	options := make([]ui.Option[projectgit.DiffSource], len(names))
	for i, name := range names {
		options[i] = ui.Option[projectgit.DiffSource]{Value: projectgit.DiffSource(i), Label: name}
	}
	has := func() bool { return p.diff.Get() != nil }
	stat := func(f func(*projectgit.DiffSnapshot) string) func() string {
		return func() string {
			if d := p.diff.Get(); d != nil {
				return f(d)
			}
			return ""
		}
	}
	state := core.NewMemo(cx, func() string {
		d := p.diff.Get()
		switch {
		case d == nil && p.diffErr.Get() != "":
			return "error"
		case d == nil:
			return "loading"
		case len(d.Files) == 0:
			return "empty"
		}
		return ""
	})
	changed := func() []projectgit.ChangedFile {
		if d := p.diff.Get(); d != nil {
			return d.Files
		}
		return nil
	}
	return core.View{TestID: "review", Style: ui.FillRest, Children: []core.Node{
		panelBar(cx,
			// The source, not its name, is the selection: names change with
			// the interface language.
			ui.Select[projectgit.DiffSource]{TestID: "review-source", Label: tr("Review source"), Options: options, Value: p.source.Get, OnChange: p.chooseSource,
				Style: core.Style{Width: core.Pt(180), FlexShrink: 1, MinWidth: core.Pt(0)}},
			core.View{Style: core.Style{FlexGrow: 1}},
			core.Show{When: has, Then: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{Direction: core.Row, Gap: 6, AlignItems: core.AlignCenter}, Children: []core.Node{
					panelSmallText(cx, stat(func(d *projectgit.DiffSnapshot) string { return fmt.Sprintf(tr("%d files"), len(d.Files)) }), muted),
					panelSmallText(cx, stat(func(d *projectgit.DiffSnapshot) string { return fmt.Sprintf("+%d", d.Added) }), success),
					panelSmallText(cx, stat(func(d *projectgit.DiffSnapshot) string { return fmt.Sprintf("−%d", d.Deleted) }), danger),
				}}
			}},
			core.Show{When: p.diffBusy.Get, Then: func(cx *core.Scope) core.Node { return ui.Spinner{Size: 12} }},
			nswidgets.ToolButton("refresh", tr("Refresh"), p.refreshDiff),
		),
		core.Show{When: func() bool { return p.diffErr.Get() != "" && p.diff.Get() != nil }, Then: func(cx *core.Scope) core.Node {
			return core.Text{TestID: "review-error", ContentFunc: p.diffErr.Get, Selectable: core.SelectableOn, StyleFunc: func() core.Style {
				return core.Style{FontSize: 11, Color: colors.Danger(), Padding: core.Symmetric(4, 10)}
			}}
		}},
		core.Dynamic(func(cx *core.Scope) core.Node {
			switch state.Get() {
			case "error":
				return panelMessage(cx, tr("Unable to read changes"), p.diffErr.GetUntracked())
			case "loading":
				return panelLoading(cx, tr("Loading changes…"))
			case "empty":
				return panelMessage(cx, tr("No changes"), names[p.diff.GetUntracked().Source]+tr(" is empty"))
			}
			return core.View{Style: ui.FillRest, Children: []core.Node{
				core.Scroll{TestID: "changed-files", Style: core.Style{MaxHeight: core.Pt(180), FlexShrink: 0}, Children: []core.Node{
					core.View{Style: core.Style{Padding: core.Symmetric(4, 8), Gap: 1}, Children: []core.Node{
						core.Each(changed, func(f projectgit.ChangedFile) string { return f.Path }, func(cx *core.Scope, get func() projectgit.ChangedFile, index func() int) core.Node {
							return p.changedFileRow(cx, get, index)
						}),
					}},
				}},
				core.View{StyleFunc: func() core.Style { return core.Style{Height: core.Pt(1), FlexShrink: 0, Background: colors.Line()} }},
				p.sidesView(cx),
			}}
		}),
	}}
}

func (p *panel) changedFileRow(cx *core.Scope, get func() projectgit.ChangedFile, index func() int) core.Node {
	colors := nswidgets.ColorsOf(cx)
	file := core.Untracked(cx, get)
	dir, name := path.Split(file.Path)
	field := func(f func(projectgit.ChangedFile) string) func() string { return func() string { return f(get()) } }
	return core.View{TestID: "changed-" + file.Path, Role: core.RoleButton, Label: file.Path,
		OnPress: func(core.PressEvent) {
			if p.diffFile.GetUntracked() != file.Path {
				p.loadDiffFile(core.Untracked(cx, index))
			}
		},
		StyleFunc: func() core.Style {
			st := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Height: core.Pt(26), Padding: core.Symmetric(0, 6), CornerRadius: 6}
			if p.diffFile.Get() == file.Path {
				st.Background = colors.Chosen()
			}
			return st
		},
		Children: []core.Node{
			nswidgets.Icon("file", 13, colors.Muted),
			core.Text{Content: name, MaxLines: 1, Style: core.Style{FontSize: 12, FlexShrink: 0}},
			core.Text{Content: strings.TrimSuffix(dir, "/"), MaxLines: 1, StyleFunc: func() core.Style {
				return core.Style{FontSize: 11, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Color: colors.Muted()}
			}},
			panelSmallText(cx, field(func(f projectgit.ChangedFile) string {
				switch {
				case f.Binary:
					return tr("Binary")
				case f.Untracked:
					return tr("New file")
				}
				return ""
			}), muted),
			core.Show{When: func() bool { return !get().Binary }, Then: func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{Direction: core.Row, Gap: 6}, Children: []core.Node{
					panelSmallText(cx, field(func(f projectgit.ChangedFile) string { return fmt.Sprintf("+%d", f.Added) }), success),
					panelSmallText(cx, field(func(f projectgit.ChangedFile) string { return fmt.Sprintf("−%d", f.Deleted) }), danger),
				}}
			}},
		},
	}
}

// sidesView shows the chosen file's changes, built again for each file so
// it starts at its top.
func (p *panel) sidesView(cx *core.Scope) core.Node {
	type shown struct {
		path, note string
		ready      bool
	}
	current := core.NewMemo(cx, func() shown {
		s := p.sides.Get()
		return shown{s.path, s.note, s.ready}
	})
	colors := nswidgets.ColorsOf(cx)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		s := current.Get()
		switch {
		case s.note != "":
			return panelMessage(cx, path.Base(s.path), s.note)
		case s.ready:
			return diff.DiffView{TestID: "review-diff", Language: s.path, Unified: true, Selectable: true,
				OldValue:  func() string { return p.sides.Get().old },
				NewValue:  func() string { return p.sides.Get().new },
				StyleFunc: func() core.Style { return ui.FillRest.Merge(core.Style{Background: colors.Canvas()}) }}
		}
		return core.View{Style: ui.FillRest.Merge(core.Style{AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}), Children: []core.Node{ui.Spinner{Size: 16}}}
	})
}

func htmlFile(rel string) bool {
	ext := path.Ext(rel)
	return strings.EqualFold(ext, ".html") || strings.EqualFold(ext, ".htm")
}

// htmlPreview shows an HTML file as a page, as MyGo does: served from the
// workspace by a file proxy, local or through the service, with the text
// being edited, in an untrusted web view that loads and navigates only
// within the proxy's exact origin. The proxy lives with the preview.
func (p *panel) htmlPreview(cx *core.Scope, ed *fileEditor) core.Node {
	root, rel := p.cwd.GetUntracked(), ed.rel
	var proxy *phoneproxy.PhoneFileProxy
	var err error
	if p.host.RemoteHost() {
		cl := p.host.State().Remote
		if cl == nil || cl.Wire() == nil {
			return panelMessage(cx, tr("Browser unavailable"), svcproto.ErrClosed.Error())
		}
		proxy, err = phoneproxy.NewPhoneFileProxy(cl.Wire(), root)
	} else {
		if !filepath.IsLocal(rel) {
			if !filepath.IsAbs(rel) {
				rel = filepath.Join(root, rel)
			}
			root, rel = filepath.Dir(rel), filepath.Base(rel)
		}
		proxy, err = phoneproxy.NewLocalFileProxy(root)
	}
	if err != nil {
		return panelMessage(cx, tr("Browser unavailable"), err.Error())
	}
	cx.Cleanup(proxy.Close)
	proxy.SetPreview(rel, core.Untracked(cx, ed.text.Get))
	origin, err := url.Parse(proxy.URL)
	if err != nil {
		return panelMessage(cx, tr("Browser unavailable"), err.Error())
	}
	allowed := origin.Scheme + "://" + origin.Host
	return core.Func(func(cx *core.Scope) core.Node {
		cx.ProvideAccess(htmlPreviewAccess{})
		client := &http.Client{
			Timeout: 4 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > 5 || req.URL.Scheme+"://"+req.URL.Host != allowed {
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
		client.Jar, _ = cookiejar.New(nil)
		const scheme = "miya-preview"
		if err := cx.ServeSchemeRequest(scheme, func(req *core.WebSchemeRequest) *core.WebSchemeResponse {
			u, err := url.Parse(req.URL)
			if err != nil || u.Host != "workspace" || u.User != nil {
				return nil
			}
			if req.Method != "" && req.Method != http.MethodGet && req.Method != http.MethodHead {
				return &core.WebSchemeResponse{Status: http.StatusMethodNotAllowed}
			}
			target := (&url.URL{Path: u.Path, RawQuery: u.RawQuery}).String()
			res, err := client.Get(proxy.URL + "?path=" + url.QueryEscape(target))
			if err != nil {
				return &core.WebSchemeResponse{Status: http.StatusBadGateway}
			}
			defer res.Body.Close()
			if res.StatusCode >= 300 && res.StatusCode < 400 {
				return &core.WebSchemeResponse{Status: http.StatusForbidden}
			}
			const maxPreviewSize = 16 << 20
			body, err := io.ReadAll(io.LimitReader(res.Body, maxPreviewSize+1))
			if err != nil || len(body) > maxPreviewSize {
				return &core.WebSchemeResponse{Status: http.StatusBadGateway}
			}
			return &core.WebSchemeResponse{Status: res.StatusCode, Body: body, Headers: map[string]string{
				"Content-Type":            res.Header.Get("Content-Type"),
				"Content-Security-Policy": "default-src 'self' data:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'self'",
			}}
		}); err != nil {
			return panelMessage(cx, tr("Browser unavailable"), err.Error())
		}
		page := (&url.URL{Scheme: scheme, Host: "workspace", Path: "/" + rel}).String()
		colors := nswidgets.ColorsOf(cx)
		return core.WebView{TestID: "file-html", Label: tr("Preview"), Style: ui.FillRest,
			Incognito: true, Source: core.WebSrc(page),
			StyleFunc: func() core.Style { return ui.FillRest.Merge(core.Style{Background: colors.Canvas()}) }}
	})
}

type htmlPreviewAccess struct{}

func (htmlPreviewAccess) WebHosts() []string   { return nil }
func (htmlPreviewAccess) WebSchemes() []string { return nil }
func (htmlPreviewAccess) Allows(req core.AccessRequest) bool {
	if req.Permission != core.AccessWebView || req.Method != "" {
		return false
	}
	u, err := url.Parse(req.Resource)
	return err == nil && u.Scheme == "miya-preview" && u.Host == "workspace" && u.User == nil
}
