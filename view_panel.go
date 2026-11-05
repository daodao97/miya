package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"miya/internal/fsutil"
)

// The right panel follows Waku's: each conversation keeps its own tabs of
// surfaces (a browser, terminals, the workspace's files and a review of its
// changes), chosen from cards while none is open.
type surfaceKind int

const (
	surfaceBrowser surfaceKind = iota
	surfaceTerminal
	surfaceFiles
	surfaceDiff
	surfaceRuntime
)

var surfaceKinds = []surfaceKind{surfaceBrowser, surfaceTerminal, surfaceFiles, surfaceDiff, surfaceRuntime}

func (k surfaceKind) label() string {
	return [...]string{tr("Browser"), tr("Terminal"), tr("Files"), tr("Review"), tr("Background tasks")}[k]
}

func (k surfaceKind) icon() string {
	return [...]string{"globe", "terminal-2", "folder", "file-diff", "stack-2"}[k]
}

func (k surfaceKind) description() string {
	return [...]string{tr("Open a local app or URL"), tr("Open a terminal in this workspace"), tr("Browse and read workspace files"), tr("Review file changes"), tr("View this conversation's tasks, processes, and services")}[k]
}

// Browsers and terminals open as many tabs as asked; the files and the
// review are one tab each.
func (k surfaceKind) single() bool {
	return k == surfaceFiles || k == surfaceDiff || k == surfaceRuntime
}

type panelSurface struct {
	runtimeData    *conversationRuntime
	runtimeLoading bool
	runtimeErr     string
	runtimeRef     ThreadRef
	runtimeAt      time.Time
	id             int
	kind           surfaceKind
	closed         bool

	web           *mygo.WebView
	address       string
	loaded        string
	webErr        string
	webProxy      *phoneFileProxy
	webGeneration int

	term     *terminal.Terminal
	termErr  string
	starting bool
}

type fileEditor struct {
	state   *editor.State
	err     string
	loading bool
	preview bool
	saveErr string
	saving  bool
	// image is the picture an image file previews as; an SVG's is parsed
	// again from its text each time the preview opens.
	image ui.ImageSource
}

// svgPreview parses an SVG's text for the preview, nil when it is not a
// valid SVG of a size.
func svgPreview(text string) ui.ImageSource {
	s, err := ui.ParseSVG([]byte(text))
	if err != nil {
		return nil
	}
	if w, h := ui.ImageSize(s); w <= 0 || h <= 0 {
		return nil
	}
	return s
}

type panelState struct {
	visible  bool
	surfaces []*panelSurface
	active   int
	cwd      string
	lastBusy bool

	selected string
	expanded map[string]bool
	dirs     map[string][]fileEntry
	dirErr   map[string]string
	loading  map[string]bool
	editors  map[string]*fileEditor
	tree     ui.ListState

	source       diffSource
	sourceName   string
	diff         *diffSnapshot
	diffErr      string
	diffBusy     bool
	diffGen      int
	diffFile     int
	diffView     *editor.DiffState
	diffViewPath string
	sideNote     string
}

func newPanelState(cwd string, visible bool) *panelState {
	p := &panelState{visible: visible, active: -1, sourceName: diffSourceNames[0]}
	p.resetWorkspace(cwd)
	return p
}

// resetWorkspace forgets what the files and the review read of the old
// workspace; terminals and browsers keep running.
func (p *panelState) resetWorkspace(cwd string) {
	p.cwd = cwd
	p.selected = ""
	p.expanded = map[string]bool{}
	p.dirs = map[string][]fileEntry{}
	p.dirErr = map[string]string{}
	p.loading = map[string]bool{}
	p.editors = map[string]*fileEditor{}
	p.diff, p.diffView, p.diffErr, p.diffViewPath, p.sideNote = nil, nil, "", "", ""
	p.diffBusy = false
	p.diffGen++
}

func (p *panelState) find(kind surfaceKind) *panelSurface {
	for _, s := range p.surfaces {
		if s.kind == kind {
			return s
		}
	}
	return nil
}

// panel is the right panel of the open conversation.
func (app *chatApp) panel() *panelState {
	if app.panels == nil {
		app.panels = map[int]*panelState{}
	}
	id := app.c.active()
	p := app.panels[id]
	if p == nil {
		p = newPanelState(app.c.cwd, app.panelVisible)
		app.panels[id] = p
	}
	if p.cwd != app.c.cwd {
		p.resetWorkspace(app.c.cwd)
	}
	return p
}

// adoptDraftPanel gives the panel of a new conversation's draft to the
// conversation its first message started.
func (app *chatApp) adoptDraftPanel(id int) {
	if p := app.panels[0]; p != nil && app.panels[id] == nil {
		app.panels[id] = p
		delete(app.panels, 0)
	}
}

func (app *chatApp) openPanel(p *panelState) {
	p.visible = true
	app.panelVisible = true
}

func (app *chatApp) hidePanel(p *panelState) {
	p.visible = false
	app.panelVisible = false
}

func (app *chatApp) closePanels() {
	for _, p := range app.panels {
		for _, s := range p.surfaces {
			closeSurfaceResources(s)
		}
	}
}

func closeSurfaceResources(s *panelSurface) {
	s.closed = true
	if s.webProxy != nil {
		s.webProxy.Close()
		s.webProxy = nil
	}
	if s.term != nil {
		t := s.term
		s.term = nil
		go t.Close()
	}
	if s.web != nil {
		s.web.Destroy()
		s.web = nil
	}
}

func (app *chatApp) openSurface(p *panelState, k surfaceKind) {
	app.openPanel(p)
	if k.single() {
		for i, s := range p.surfaces {
			if s.kind == k {
				p.active = i
				if k == surfaceDiff {
					app.refreshDiff(p)
				}
				return
			}
		}
	}
	app.nextSurface++
	s := &panelSurface{id: app.nextSurface, kind: k}
	p.surfaces = append(p.surfaces, s)
	p.active = len(p.surfaces) - 1
	switch k {
	case surfaceBrowser:
		app.startBrowser(s)
	case surfaceTerminal:
		app.startTerminal(s, p.cwd)
	case surfaceFiles:
		app.loadDir(p, "")
	case surfaceDiff:
		app.refreshDiff(p)
	}
}

func (app *chatApp) closeSurface(p *panelState, i int) {
	if i < 0 || i >= len(p.surfaces) {
		return
	}
	closeSurfaceResources(p.surfaces[i])
	p.surfaces = append(p.surfaces[:i:i], p.surfaces[i+1:]...)
	switch {
	case p.active >= len(p.surfaces):
		p.active = len(p.surfaces) - 1
	case p.active > i:
		p.active--
	}
}

func (app *chatApp) surfaceLabel(p *panelState, s *panelSurface) (label, symbol string) {
	label, symbol = s.kind.label(), s.kind.icon()
	switch s.kind {
	case surfaceFiles:
		if p.selected != "" {
			label, symbol = path.Base(p.selected), "file"
		}
	case surfaceBrowser:
		if u, err := url.Parse(s.loaded); err == nil && u.Host != "" {
			label = u.Host
		}
	}
	return label, symbol
}

func (p *panelState) dirty(s *panelSurface) bool {
	if s.kind != surfaceFiles {
		return false
	}
	for _, ed := range p.editors {
		if ed.state != nil && ed.state.Buffer.Modified() {
			return true
		}
	}
	return false
}

// rightPanel is the panel beside the conversation, with a divider that
// resizes it.
func (app *chatApp) rightPanel(c *ui.Context, p *panelState) {
	if app.panelWidth <= 0 {
		app.panelWidth = 460
	}
	// A turn that ends may have changed the workspace: read it again.
	if cur := app.c.current(); p.lastBusy && !cur.Busy {
		app.workspaceChanged(p)
	}
	p.lastBusy = app.c.current().Busy

	divider := ui.Box(c.Key("panel-divider")).Width(5).FillHeight().Shrink(0).Cursor(ui.CursorResizeEW).Label(tr("Resize right panel"))
	if dx, _, ok := divider.Dragged(); ok {
		app.panelWidth = min(max(app.panelWidth-dx, 320), 960)
	}
	divider.Children(func() {
		color := line
		if divider.Hovered() || divider.Dragging() {
			color = muted
		}
		ui.Box(c).Width(1).FillHeight().Margin(0, 0, 0, 4).Background(color)
	})
	ui.Column(c.Key("right-panel")).Width(app.panelWidth).Shrink(0).FillHeight().Background(side).Clip().Children(func() {
		app.panelHeader(c, p)
		if p.active < 0 || p.active >= len(p.surfaces) {
			app.panelChooser(c, p)
			return
		}
		s := p.surfaces[p.active]
		c := c.Key(s.id)
		switch s.kind {
		case surfaceBrowser:
			app.browserView(c, s)
		case surfaceTerminal:
			app.terminalView(c, p, s)
		case surfaceFiles:
			app.filesView(c, p)
		case surfaceDiff:
			app.diffSurface(c, p)
		case surfaceRuntime:
			app.runtimeSurface(c, p, s)
		}
	})
}

func (app *chatApp) workspaceChanged(p *panelState) {
	if p.find(surfaceDiff) != nil {
		app.refreshDiff(p)
	}
	for rel := range p.dirs {
		app.loadDir(p, rel)
	}
}

func (app *chatApp) panelHeader(c *ui.Context, p *panelState) {
	ui.Row(c).Height(56).Shrink(0).AlignItems(ui.Center).Gap(6).Padding(0, 14, 0, 10).DragWindow().Children(func() {
		ui.ScrollHorizontal(c.Key("tabs")).Grow(1).MinWidth(0).Scrollbars(false).Label(tr("Right panel tabs")).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				for i, s := range p.surfaces {
					app.panelTab(c.Key(s.id), p, i, s)
				}
			})
		})
		if len(p.surfaces) > 0 {
			add := toolButton(c.Key("add-surface"), "plus", tr("Add panel tab"))
			add.PopoverMenuButton(func(m *ui.Menu) {
				for _, k := range surfaceKinds {
					item := m.Item(k.label())
					if k.single() && p.find(k) != nil {
						item.Checked(true)
					}
					if item.Chosen() {
						app.openSurface(p, k)
					}
				}
			})
		}
		if toolButton(c.Key("hide-panel"), "layout-sidebar-right", tr("Hide right panel")).Tooltip(tr("Hide right panel · ") + shortcutKeys(ui.Cmd|ui.Alt, "B")).Clicked() {
			app.hidePanel(p)
		}
	})
}

func (app *chatApp) panelTab(c *ui.Context, p *panelState, i int, s *panelSurface) {
	active := i == p.active
	label, symbol := app.surfaceLabel(p, s)
	tab := ui.ButtonBase(c).Row().AlignItems(ui.Center).Gap(6).Height(28).MinWidth(100).MaxWidth(176).PaddingX(8).Radius(6).Label(label)
	if active {
		tab.Background(chosen)
	} else if tab.Hovered() {
		tab.Background(hover)
	}
	tab.Children(func() {
		icon(c, symbol, 13, muted)
		color := muted
		if active {
			color = ink
		}
		ui.Text(c, label).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12).TextColor(color)
		if p.dirty(s) {
			ui.Box(c.Key("dirty")).Size(7, 7).Radius(4).Shrink(0).Background(ui.Hex("#e0a030")).Tooltip(tr("Unsaved changes. Press ⌘S to save."))
		}
		x := ui.ButtonBase(c.Key("close")).Size(16, 16).Shrink(0).Radius(4).Justify(ui.Center).AlignItems(ui.Center).Label(tr("Close") + label)
		if x.Hovered() {
			x.Background(chosen)
		}
		x.Children(func() { icon(c, "x", 10, muted) })
		x.OnClick(func() { app.closeSurface(p, i) })
	})
	if tab.Clicked() {
		p.active = i
	}
}

func (app *chatApp) panelChooser(c *ui.Context, p *panelState) {
	ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Center).Justify(ui.Center).Padding(0, 20, 32, 20).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(420).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Open a panel")).FontSize(13).FontWeight(500)
			ui.Text(c, tr("Choose what to show in the right panel")).FontSize(12).TextColor(muted).Margin(5, 0, 0, 0)
			for row, kinds := range [][]surfaceKind{{surfaceBrowser, surfaceTerminal}, {surfaceFiles, surfaceDiff}, {surfaceRuntime}} {
				ui.Row(c.Key(row)).FillWidth().Gap(8).Margin(map[bool]float32{true: 18, false: 8}[row == 0], 0, 0, 0).Children(func() {
					for _, k := range kinds {
						app.surfaceCard(c.Key(k), p, k)
					}
				})
			}
		})
	})
}

func (app *chatApp) surfaceCard(c *ui.Context, p *panelState, k surfaceKind) {
	card := ui.ButtonBase(c).Column().AlignItems(ui.Start).Height(112).Grow(1).Basis(0).MinWidth(0).Padding(14).Radius(8).
		Border(1, border).Background(panel).Label(k.label())
	if card.Hovered() {
		card.Background(canvas).BorderColor(muted)
	}
	card.Children(func() {
		icon(c, k.icon(), 18, muted)
		ui.Text(c, k.label()).FontSize(12).FontWeight(500).Margin(12, 0, 0, 0)
		ui.Text(c, k.description()).FontSize(12).TextColor(muted).MaxLines(2).Margin(4, 0, 0, 0)
	})
	if card.Clicked() {
		app.openSurface(p, k)
	}
}

func panelMessage(c *ui.Context, title, detail string) {
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(6).Padding(24).Children(func() {
		ui.Text(c, title).FontWeight(500)
		if detail != "" {
			ui.Text(c, detail).FontSize(12).TextColor(muted).TextAlign(ui.Center)
		}
	})
}

func panelBar(c *ui.Context, fn func()) {
	ui.Row(c).Height(38).Shrink(0).Padding(0, 10).Gap(4).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(line).Children(fn)
}

// Browser

func (app *chatApp) startBrowser(s *panelSurface) {
	w := currentWindow()
	if w == nil {
		s.webErr = tr("The browser requires an app window")
		return
	}
	web, err := w.NewWebView(mygo.WebViewOptions{BackgroundColor: "#ffffff"})
	if err != nil {
		s.webErr = err.Error()
		return
	}
	s.web = web
}

func (app *chatApp) navigate(s *panelSurface) {
	target := browserURL(s.address)
	if target == "" || s.web == nil {
		return
	}
	s.address, s.loaded = target, target
	if app.c.remoteHost() {
		app.navigateRemoteBrowser(s, target, phoneWebService{})
		return
	}
	if err := s.web.Page().LoadURL(target); err != nil {
		s.webErr = err.Error()
	}
}

func (app *chatApp) browserView(c *ui.Context, s *panelSurface) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		panelBar(c, func() {
			if s.web != nil {
				if toolButton(c.Key("back"), "arrow-left", tr("Back")).Clicked() {
					s.web.Page().GoBack()
				}
				if toolButton(c.Key("forward"), "arrow-right", tr("Forward")).Clicked() {
					s.web.Page().GoForward()
				}
				if toolButton(c.Key("reload"), "refresh", tr("Reload")).Clicked() {
					s.web.Page().Reload()
				}
			}
			in := ui.TextInput(c.Key("address"), &s.address).Placeholder(tr("Enter a URL or localhost port")).Label(tr("Address")).Grow(1).FontSize(12)
			if s.loaded == "" {
				in.AutoFocus()
			}
			if in.Submitted() {
				app.navigate(s)
			}
		})
		switch {
		case s.webErr != "":
			panelMessage(c, tr("Browser unavailable"), s.webErr)
		case s.web != nil:
			ui.WebView(c, s.web).Grow(1)
		}
	})
}

// Terminal

func (app *chatApp) startTerminal(s *panelSurface, dir string) {
	s.starting, s.termErr = true, ""
	var cl *serviceClient
	if app.c.remoteHost() {
		cl = app.c.remote
	}
	goAsync(func() {
		opts := terminal.Options{
			Dir:                     dir,
			ShellIntegration:        true,
			CopyOnSelect:            terminal.CopyOnSelectClipboard,
			SelectOnDrag:            true,
			SelectOnDragInAltScreen: true,
		}
		var err error
		if cl != nil {
			opts.Conn, err = openRemoteTerminal(cl, dir)
		}
		var t *terminal.Terminal
		if err == nil {
			t, err = terminal.New(opts)
		}
		if err != nil && opts.Conn != nil {
			opts.Conn.Close()
		}
		app.c.dispatch(func() {
			s.starting = false
			if err != nil {
				s.termErr = err.Error()
				return
			}
			if s.closed {
				go t.Close()
				return
			}
			s.term = t
			go func() {
				<-t.Done()
				invalidate()
			}()
		})
	})
}

func terminalExited(t *terminal.Terminal) bool {
	select {
	case <-t.Done():
		return true
	default:
		return false
	}
}

func (app *chatApp) terminalView(c *ui.Context, p *panelState, s *panelSurface) {
	restart := func(title, detail string) {
		ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Padding(24).Children(func() {
			ui.Text(c, title).FontWeight(500)
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(muted).TextAlign(ui.Center)
			}
			if ui.Button(c.Key("restart"), tr("Restart")).Clicked() {
				if s.term != nil {
					go s.term.Close()
					s.term = nil
				}
				app.startTerminal(s, p.cwd)
			}
		})
	}
	switch {
	case s.starting:
		ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Spinner(c)
			mutedText(c, tr("Starting terminal…"))
		})
	case s.termErr != "":
		restart(tr("Terminal unavailable"), s.termErr)
	case s.term == nil:
		restart(tr("Terminal unavailable"), tr("Close and reopen the terminal panel"))
	case terminalExited(s.term):
		restart(tr("Terminal exited"), fmt.Sprintf(tr("Exit code %d"), s.term.ExitCode()))
	default:
		ui.Box(c).Grow(1).MinHeight(0).Background(canvas).Padding(6, 4, 4, 8).Children(func() {
			// Keep the terminal distinct from the loading state's children so
			// AutoFocus runs when the asynchronously created terminal appears.
			terminal.View(c.Key("terminal"), s.term).Fill().AutoFocus()
		})
	}
}

// Files

type treeRow struct {
	entry fileEntry
	depth int
}

func (p *panelState) treeRows() []treeRow {
	var rows []treeRow
	var visit func(rel string, depth int)
	visit = func(rel string, depth int) {
		for _, e := range p.dirs[rel] {
			rows = append(rows, treeRow{entry: e, depth: depth})
			if e.dir && p.expanded[e.rel] {
				visit(e.rel, depth+1)
			}
		}
	}
	visit("", 0)
	return rows
}

func (app *chatApp) loadDir(p *panelState, rel string) {
	if p.loading[rel] {
		return
	}
	p.loading[rel] = true
	root := p.cwd
	var cl *serviceClient
	if app.c.remoteHost() {
		cl = app.c.remote
	}
	goAsync(func() {
		var entries []fileEntry
		var err error
		if cl == nil {
			entries, err = readDirEntries(root, rel)
		} else {
			var rows []hostEntry
			err = cl.call("host", hostRequest{Action: "dir", Project: root, Path: rel}, &rows)
			for _, e := range rows {
				entries = append(entries, fileEntry{e.Name, e.Path, e.Dir})
			}
		}
		app.c.dispatch(func() {
			if p.cwd != root {
				return
			}
			delete(p.loading, rel)
			if err != nil {
				p.dirErr[rel] = err.Error()
				delete(p.dirs, rel)
				return
			}
			delete(p.dirErr, rel)
			p.dirs[rel] = entries
		})
	})
}

func (app *chatApp) openFile(p *panelState, rel string) {
	p.selected = rel
	// An editor already open keeps its unsaved edits.
	if ed := p.editors[rel]; ed != nil && ed.err == "" {
		return
	}
	kind := imageFileKind(rel)
	ed := &fileEditor{loading: true, preview: strings.EqualFold(path.Ext(rel), ".md") || kind != ""}
	p.editors[rel] = ed
	root := p.cwd
	var cl *serviceClient
	if app.c.remoteHost() {
		cl = app.c.remote
	}
	goAsync(func() {
		var text string
		var img ui.ImageSource
		var err error
		if kind == "bitmap" {
			var data []byte
			if cl == nil {
				data, err = readImageFile(filepath.Join(root, rel))
			} else {
				err = cl.call("host", hostRequest{Action: "image", Project: root, Path: rel}, &data)
			}
			if err == nil {
				if img, err = ui.DecodeImage(data); err != nil {
					err = fmt.Errorf(tr("Unable to decode image: %w"), err)
				}
			}
		} else {
			if cl == nil {
				text, err = readTextFile(filepath.Join(root, rel))
			} else {
				err = cl.call("host", hostRequest{Action: "read", Project: root, Path: rel}, &text)
			}
			if err == nil && kind == "svg" {
				img = svgPreview(text)
			}
		}
		app.c.dispatch(func() {
			ed.loading = false
			if err != nil {
				ed.err = err.Error()
				return
			}
			ed.image = img
			if kind == "bitmap" {
				return
			}
			buf := editor.NewBuffer(text)
			buf.SetLanguage(editor.LanguageFor(rel))
			ed.state = &editor.State{Buffer: buf, Label: rel}
		})
	})
}

func (app *chatApp) saveFile(p *panelState, rel string, ed *fileEditor) {
	if ed.state == nil || !ed.state.Buffer.Modified() || ed.saving {
		return
	}
	buf := ed.state.Buffer
	if app.c.remoteHost() {
		text, root, cl := buf.String(), p.cwd, app.c.remote
		ed.saving, ed.saveErr = true, ""
		goAsync(func() {
			err := cl.call("host", hostRequest{Action: "write", Project: root, Path: rel, Text: text}, nil)
			app.c.dispatch(func() {
				ed.saving = false
				if p.cwd != root {
					return
				}
				if err != nil {
					ed.saveErr = tr("Unable to save: ") + err.Error()
					return
				}
				ed.saveErr = ""
				if buf.String() == text {
					buf.MarkSaved()
				}
				if p.find(surfaceDiff) != nil {
					app.refreshDiff(p)
				}
			})
		})
		return
	}
	err := fsutil.WriteFileAtomic(filepath.Join(p.cwd, rel), func(f *os.File) error {
		_, err := buf.WriteTo(f)
		return err
	})
	if err != nil {
		ed.saveErr = tr("Unable to save: ") + err.Error()
		return
	}
	ed.saveErr = ""
	buf.MarkSaved()
	if p.find(surfaceDiff) != nil {
		app.refreshDiff(p)
	}
}

func (app *chatApp) filesView(c *ui.Context, p *panelState) {
	if p.selected != "" {
		app.fileEditorView(c.Key("file:"+p.selected), p, p.selected)
		return
	}
	rows := p.treeRows()
	p.tree.Key = func(i int) any { return rows[i].entry.rel }
	p.tree.Label = func(i int) string { return rows[i].entry.name }
	ui.List(c.Key("tree"), &p.tree, len(rows), func(i int) {
		app.treeRowView(c, p, rows[i])
	}).Grow(1).MinHeight(0).Padding(4, 8).Children(func() {
		switch {
		case p.dirErr[""] != "":
			panelMessage(c, tr("Unable to read workspace"), p.dirErr[""])
		case p.loading[""] && len(rows) == 0:
			ui.Row(c).Gap(8).Margin(ui.Auto).Children(func() {
				ui.Spinner(c)
				mutedText(c, tr("Loading files…"))
			})
		case len(rows) == 0:
			panelMessage(c, tr("This folder is empty"), p.cwd)
		}
	})
}

func (app *chatApp) treeRowView(c *ui.Context, p *panelState, r treeRow) {
	e := r.entry
	row := quiet(c).FillWidth().Height(26).Gap(6).PaddingX(0).PaddingStart(6 + float32(r.depth)*14).Label(e.name)
	row.Children(func() {
		if e.dir {
			ch := icon(c, "chevron-right", 12, muted)
			ch.Rotate(ch.Animate("open", map[bool]float32{false: 0, true: 90}[p.expanded[e.rel]], 120*time.Millisecond))
			icon(c, "folder", 14, faint)
		} else {
			ui.Box(c).Width(12).Shrink(0)
			icon(c, "file", 14, muted)
		}
		ui.Text(c, e.name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12)
		if e.dir && p.loading[e.rel] {
			ui.Spinner(c).FontSize(11)
		}
	})
	if !row.Clicked() {
		return
	}
	if !e.dir {
		app.openFile(p, e.rel)
		return
	}
	p.expanded[e.rel] = !p.expanded[e.rel]
	if p.expanded[e.rel] {
		app.loadDir(p, e.rel)
	}
}

func (app *chatApp) fileEditorView(c *ui.Context, p *panelState, rel string) {
	ed := p.editors[rel]
	if ed == nil {
		p.selected = ""
		return
	}
	markdownFile := strings.EqualFold(path.Ext(rel), ".md")
	imageKind := imageFileKind(rel)
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		panelBar(c, func() {
			if toolButton(c.Key("back"), "arrow-left", tr("Back to file list")).Clicked() {
				p.selected = ""
			}
			ui.Text(c, rel).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12).TextColor(muted)
			if (markdownFile || imageKind == "svg") && ed.state != nil {
				label, symbol := tr("Preview"), "eye"
				if ed.preview {
					label, symbol = tr("Source"), "code"
				}
				if toolButton(c.Key("preview"), symbol, label).Clicked() {
					ed.preview = !ed.preview
					if ed.preview && imageKind == "svg" {
						ed.image = svgPreview(ed.state.Buffer.String())
					}
				}
			}
			if ed.state != nil && ed.state.Buffer.Modified() {
				if toolButton(c.Key("save"), "device-floppy", tr("Save (⌘S)")).Disabled(ed.saving).Clicked() {
					app.saveFile(p, rel, ed)
				}
			}
		})
		if ed.saveErr != "" {
			ui.Text(c, ed.saveErr).FontSize(11).TextColor(danger).Padding(4, 10)
		}
		if ed.saving {
			ui.Text(c, tr("Saving…")).FontSize(11).TextColor(muted).Padding(4, 10)
		}
		switch {
		case ed.loading:
			ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Spinner(c)
				mutedText(c, tr("Loading files…"))
			})
		case ed.err != "":
			panelMessage(c, tr("Unable to open this file"), ed.err)
		case ed.preview && imageKind != "" && ed.image == nil:
			panelMessage(c, tr("Unable to preview this image"), tr("Invalid SVG content or missing dimensions. Switch to source to view it."))
		case ed.preview && imageKind != "":
			w, h := ui.ImageSize(ed.image)
			ui.Column(c.Key("image")).Grow(1).MinHeight(0).Padding(16).Gap(8).Background(canvas).Children(func() {
				ui.Column(c).Grow(1).MinHeight(0).Children(func() {
					ui.Image(c, ed.image).Fill().Fit(ui.ScaleDown).Label(path.Base(rel))
				})
				ui.Text(c, fmt.Sprintf("%d × %d", int(w+0.5), int(h+0.5))).FontSize(11).TextColor(muted).TextAlign(ui.Center).FillWidth()
			})
		case ed.preview && ed.state != nil:
			ui.Scroll(c.Key("preview")).Grow(1).MinHeight(0).Background(canvas).Children(func() {
				ui.Column(c).Padding(16, 20).Children(func() {
					markdown.View(c, ed.state.Buffer.String(), &app.md)
				})
			})
		default:
			editor.View(c.Key("editor"), ed.state).Grow(1).FillWidth().Background(canvas).
				OnShortcut(ui.Cmd, ui.KeyS, func() { app.saveFile(p, rel, ed) })
		}
	})
}

// Review

func (app *chatApp) refreshDiff(p *panelState) {
	p.diffGen++
	gen, root, src := p.diffGen, p.cwd, p.source
	previous := p.diffViewPath
	p.diffBusy = true
	parent := app.c.ctx
	if parent == nil {
		parent = context.Background()
	}
	var cl *serviceClient
	if app.c.remoteHost() {
		cl = app.c.remote
	}
	goAsync(func() {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		var snap diffSnapshot
		var err error
		if cl == nil {
			snap, err = collectDiff(ctx, root, src)
		} else {
			err = cl.call("host", hostRequest{Action: "diff", Project: root, Source: src}, &snap)
		}
		app.c.dispatch(func() {
			if p.diffGen != gen || p.cwd != root {
				return
			}
			p.diffBusy = false
			if err != nil {
				if p.diff == nil {
					p.diffErr = err.Error()
				} else {
					p.diffErr = tr("Refresh failed: ") + err.Error()
				}
				return
			}
			p.diffErr = ""
			p.diff = &snap
			p.diffFile = 0
			for i, f := range snap.Files {
				if f.Path == previous {
					p.diffFile = i
				}
			}
			p.diffView, p.diffViewPath, p.sideNote = nil, "", ""
			if len(snap.Files) > 0 {
				app.loadDiffFile(p, p.diffFile)
			}
		})
	})
}

func (app *chatApp) loadDiffFile(p *panelState, i int) {
	if p.diff == nil || i < 0 || i >= len(p.diff.Files) {
		return
	}
	p.diffFile = i
	f, snap, gen, root := p.diff.Files[i], *p.diff, p.diffGen, p.cwd
	p.diffViewPath = f.Path
	if f.Binary {
		p.diffView, p.sideNote = nil, tr("Binary file; no diff available")
		return
	}
	p.sideNote = ""
	parent := app.c.ctx
	if parent == nil {
		parent = context.Background()
	}
	var cl *serviceClient
	if app.c.remoteHost() {
		cl = app.c.remote
	}
	goAsync(func() {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		var old, new string
		var err error
		if cl == nil {
			old, new = fileSides(ctx, root, snap, f)
		} else {
			var sides hostSides
			err = cl.call("host", hostRequest{Action: "sides", Project: root, Snapshot: snap, File: f}, &sides)
			old, new = sides.Old, sides.New
		}
		app.c.dispatch(func() {
			if p.diffGen != gen || p.diffViewPath != f.Path {
				return
			}
			if err != nil {
				p.sideNote = err.Error()
				return
			}
			lang := editor.LanguageFor(f.Path)
			before, after := editor.NewBuffer(old), editor.NewBuffer(new)
			before.SetLanguage(lang)
			after.SetLanguage(lang)
			p.diffView = &editor.DiffState{Old: before, New: after, Layout: editor.DiffUnified}
		})
	})
}

func (app *chatApp) diffSurface(c *ui.Context, p *panelState) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		panelBar(c, func() {
			if ui.Select(c.Key("source"), &p.sourceName, diffSourceNames).Label(tr("Review source")).FontSize(12).Changed() {
				for i, name := range diffSourceNames {
					if name == p.sourceName {
						p.source = diffSource(i)
					}
				}
				p.diff, p.diffView, p.diffViewPath, p.diffErr = nil, nil, "", ""
				app.refreshDiff(p)
			}
			ui.Spacer(c)
			if p.diff != nil {
				ui.Text(c, fmt.Sprintf(tr("%d files"), len(p.diff.Files))).FontSize(11).TextColor(muted)
				ui.Text(c, fmt.Sprintf("+%d", p.diff.Added)).FontSize(11).TextColor(success)
				ui.Text(c, fmt.Sprintf("−%d", p.diff.Deleted)).FontSize(11).TextColor(danger)
			}
			if p.diffBusy {
				ui.Spinner(c).FontSize(12)
			}
			if toolButton(c.Key("refresh"), "refresh", tr("Refresh")).Clicked() {
				app.refreshDiff(p)
			}
		})
		if p.diffErr != "" && p.diff != nil {
			ui.Text(c, p.diffErr).FontSize(11).TextColor(danger).Padding(4, 10)
		}
		switch {
		case p.diff == nil && p.diffErr != "":
			panelMessage(c, tr("Unable to read changes"), p.diffErr)
			return
		case p.diff == nil:
			ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Spinner(c)
				mutedText(c, tr("Loading changes…"))
			})
			return
		case len(p.diff.Files) == 0:
			panelMessage(c, tr("No changes"), diffSourceNames[p.diff.Source]+tr(" is empty"))
			return
		}
		ui.Scroll(c.Key("changed-files")).MaxHeight(180).Shrink(0).Children(func() {
			ui.Column(c).Padding(4, 8).Gap(1).Children(func() {
				for i, f := range p.diff.Files {
					app.changedFileRow(c.Key(f.Path), p, i, f)
				}
			})
		})
		ui.Box(c).Height(1).Shrink(0).Background(line)
		switch {
		case p.sideNote != "":
			panelMessage(c, path.Base(p.diffViewPath), p.sideNote)
		case p.diffView != nil:
			editor.DiffView(c.Key("diff:"+p.diffViewPath), p.diffView).Grow(1).FillWidth().Background(canvas)
		default:
			ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Children(func() { ui.Spinner(c) })
		}
	})
}

func (app *chatApp) changedFileRow(c *ui.Context, p *panelState, i int, f changedFile) {
	row := quiet(c).FillWidth().Height(26).Gap(6).PaddingX(6).Label(f.Path)
	if i == p.diffFile {
		row.Background(chosen)
	}
	row.Children(func() {
		icon(c, "file", 13, muted)
		dir, name := path.Split(f.Path)
		ui.Text(c, name).Shrink(0).FontSize(12)
		ui.Text(c, strings.TrimSuffix(dir, "/")).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(11).TextColor(muted)
		switch {
		case f.Binary:
			ui.Text(c, tr("Binary")).FontSize(11).TextColor(muted)
		default:
			if f.Untracked {
				ui.Text(c, tr("New file")).FontSize(11).TextColor(muted)
			}
			ui.Text(c, fmt.Sprintf("+%d", f.Added)).FontSize(11).TextColor(success)
			ui.Text(c, fmt.Sprintf("−%d", f.Deleted)).FontSize(11).TextColor(danger)
		}
	})
	if row.Clicked() && i != p.diffFile {
		app.loadDiffFile(p, i)
	}
}
