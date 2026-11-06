package desktopui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"miya/internal/chat/model"
	"miya/internal/fsutil"
	phoneproxy "miya/internal/phone/proxy"
	projectfiles "miya/internal/project/files"
	projectgit "miya/internal/project/git"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

// The right panel follows Waku's: each conversation keeps its own tabs of
// surfaces (a browser, terminals, the workspace's files and a review of its
// changes), chosen from cards while none is open.
type surfaceKind int

const (
	surfaceBrowser surfaceKind = iota
	SurfaceTerminal
	SurfaceFiles
	surfaceDiff
	SurfaceRuntime
)

var surfaceKinds = []surfaceKind{surfaceBrowser, SurfaceTerminal, SurfaceFiles, surfaceDiff, SurfaceRuntime}

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
	return k == SurfaceFiles || k == surfaceDiff || k == SurfaceRuntime
}

type PanelSurface struct {
	RuntimeData    *tracker.ConversationRuntime
	runtimeLoading bool
	runtimeErr     string
	RuntimeRef     model.ThreadRef
	RuntimeAt      time.Time
	Id             int
	Kind           surfaceKind
	closed         bool

	web           *mygo.WebView
	address       string
	loaded        string
	webErr        string
	webProxy      *phoneproxy.PhoneFileProxy
	webGeneration int

	Term     *terminal.Terminal
	termErr  string
	Starting bool
}

type fileEditor struct {
	State   *editor.State
	err     string
	loading bool
	Preview bool
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
	Visible  bool
	Surfaces []*PanelSurface
	Active   int
	Cwd      string
	lastBusy bool

	Selected string
	expanded map[string]bool
	dirs     map[string][]projectfiles.FileEntry
	dirErr   map[string]string
	loading  map[string]bool
	Editors  map[string]*fileEditor
	tree     ui.ListState

	source       projectgit.DiffSource
	sourceName   string
	Diff         *projectgit.DiffSnapshot
	DiffErr      string
	diffBusy     bool
	diffGen      int
	diffFile     int
	DiffView     *editor.DiffState
	DiffViewPath string
	sideNote     string
}

func newPanelState(cwd string, visible bool) *panelState {
	p := &panelState{Visible: visible, Active: -1, sourceName: projectgit.DiffSourceNames()[0]}
	p.resetWorkspace(cwd)
	return p
}

// resetWorkspace forgets what the files and the review read of the old
// workspace; terminals and browsers keep running.
func (p *panelState) resetWorkspace(cwd string) {
	p.Cwd = cwd
	p.Selected = ""
	p.expanded = map[string]bool{}
	p.dirs = map[string][]projectfiles.FileEntry{}
	p.dirErr = map[string]string{}
	p.loading = map[string]bool{}
	p.Editors = map[string]*fileEditor{}
	p.Diff, p.DiffView, p.DiffErr, p.DiffViewPath, p.sideNote = nil, nil, "", "", ""
	p.diffBusy = false
	p.diffGen++
}

func (p *panelState) find(kind surfaceKind) *PanelSurface {
	for _, s := range p.Surfaces {
		if s.Kind == kind {
			return s
		}
	}
	return nil
}

// panel is the right panel of the open conversation.
func (app *App) Panel() *panelState {
	if app.panels == nil {
		app.panels = map[int]*panelState{}
	}
	id := app.C.State().Active()
	p := app.panels[id]
	if p == nil {
		p = newPanelState(app.C.State().Cwd, app.PanelVisible)
		app.panels[id] = p
	}
	if p.Cwd != app.C.State().Cwd {
		p.resetWorkspace(app.C.State().Cwd)
		// Open files and review views follow the new workspace; otherwise
		// they stay empty until reopened.
		if p.find(SurfaceFiles) != nil {
			app.loadDir(p, "")
		}
		if p.find(surfaceDiff) != nil {
			app.refreshDiff(p)
		}
	}
	return p
}

// adoptDraftPanel gives the panel of a new conversation's draft to the
// conversation its first message started.
func (app *App) adoptDraftPanel(id int) {
	if p := app.panels[0]; p != nil && app.panels[id] == nil {
		app.panels[id] = p
		delete(app.panels, 0)
	}
}

func (app *App) openPanel(p *panelState) {
	p.Visible = true
	app.PanelVisible = true
}

func (app *App) HidePanel(p *panelState) {
	p.Visible = false
	app.PanelVisible = false
}

func (app *App) closePanels() {
	for _, p := range app.panels {
		for _, s := range p.Surfaces {
			closeSurfaceResources(s)
		}
	}
}

func closeSurfaceResources(s *PanelSurface) {
	s.closed = true
	if s.webProxy != nil {
		s.webProxy.Close()
		s.webProxy = nil
	}
	if s.Term != nil {
		t := s.Term
		s.Term = nil
		go t.Close()
	}
	if s.web != nil {
		s.web.Destroy()
		s.web = nil
	}
}

func (app *App) openSurface(p *panelState, k surfaceKind) {
	app.openPanel(p)
	if k.single() {
		for i, s := range p.Surfaces {
			if s.Kind == k {
				p.Active = i
				if k == surfaceDiff {
					app.refreshDiff(p)
				}
				return
			}
		}
	}
	app.nextSurface++
	s := &PanelSurface{Id: app.nextSurface, Kind: k}
	p.Surfaces = append(p.Surfaces, s)
	p.Active = len(p.Surfaces) - 1
	switch k {
	case surfaceBrowser:
		app.startBrowser(s)
	case SurfaceTerminal:
		app.startTerminal(s, p.Cwd)
	case SurfaceFiles:
		app.loadDir(p, "")
	case surfaceDiff:
		app.refreshDiff(p)
	}
}

func (app *App) closeSurface(p *panelState, i int) {
	if i < 0 || i >= len(p.Surfaces) {
		return
	}
	closeSurfaceResources(p.Surfaces[i])
	p.Surfaces = append(p.Surfaces[:i:i], p.Surfaces[i+1:]...)
	switch {
	case p.Active >= len(p.Surfaces):
		p.Active = len(p.Surfaces) - 1
	case p.Active > i:
		p.Active--
	}
}

func (app *App) surfaceLabel(p *panelState, s *PanelSurface) (label, symbol string) {
	label, symbol = s.Kind.label(), s.Kind.icon()
	switch s.Kind {
	case SurfaceFiles:
		if p.Selected != "" {
			label, symbol = path.Base(p.Selected), "file"
		}
	case surfaceBrowser:
		if u, err := url.Parse(s.loaded); err == nil && u.Host != "" {
			label = u.Host
		}
	}
	return label, symbol
}

func (p *panelState) Dirty(s *PanelSurface) bool {
	if s.Kind != SurfaceFiles {
		return false
	}
	for _, ed := range p.Editors {
		if ed.State != nil && ed.State.Buffer.Modified() {
			return true
		}
	}
	return false
}

// rightPanel is the panel beside the conversation, with a divider that
// resizes it.
func (app *App) rightPanel(c *ui.Context, p *panelState) {
	if app.panelWidth <= 0 {
		app.panelWidth = 460
	}
	// A turn that ends may have changed the workspace: read it again.
	if cur := app.C.Current(); p.lastBusy && !cur.Busy {
		app.workspaceChanged(p)
	}
	p.lastBusy = app.C.Current().Busy

	divider := ui.Box(c.Key("panel-divider")).Width(5).FillHeight().Shrink(0).Cursor(ui.CursorResizeEW).Label(tr("Resize right panel"))
	if dx, _, ok := divider.Dragged(); ok {
		app.panelWidth = min(max(app.panelWidth-dx, 320), 960)
	}
	divider.Children(func() {
		color := widgets.Line
		if divider.Hovered() || divider.Dragging() {
			color = widgets.Muted
		}
		ui.Box(c).Width(1).FillHeight().Margin(0, 0, 0, 4).Background(color)
	})
	ui.Column(c.Key("right-panel")).Width(app.panelWidth).Shrink(0).FillHeight().Background(widgets.Side).Clip().Children(func() {
		app.panelHeader(c, p)
		if p.Active < 0 || p.Active >= len(p.Surfaces) {
			app.panelChooser(c, p)
			return
		}
		s := p.Surfaces[p.Active]
		c := c.Key(s.Id)
		switch s.Kind {
		case surfaceBrowser:
			app.browserView(c, s)
		case SurfaceTerminal:
			app.terminalView(c, p, s)
		case SurfaceFiles:
			app.filesView(c, p)
		case surfaceDiff:
			app.diffSurface(c, p)
		case SurfaceRuntime:
			app.runtimeSurface(c, p, s)
		}
	})
}

func (app *App) workspaceChanged(p *panelState) {
	if p.find(surfaceDiff) != nil {
		app.refreshDiff(p)
	}
	for rel := range p.dirs {
		app.loadDir(p, rel)
	}
}

func (app *App) panelHeader(c *ui.Context, p *panelState) {
	ui.Row(c).Height(56).Shrink(0).AlignItems(ui.Center).Gap(6).Padding(0, 14, 0, 10).DragWindow().Children(func() {
		ui.ScrollHorizontal(c.Key("tabs")).Grow(1).MinWidth(0).Scrollbars(false).Label(tr("Right panel tabs")).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				for i, s := range p.Surfaces {
					app.panelTab(c.Key(s.Id), p, i, s)
				}
			})
		})
		if len(p.Surfaces) > 0 {
			add := widgets.ToolButton(c.Key("add-surface"), "plus", tr("Add panel tab"))
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
		if widgets.ToolButton(c.Key("hide-panel"), "layout-sidebar-right", tr("Hide right panel")).Tooltip(tr("Hide right panel · ") + ShortcutKeys(ui.Cmd|ui.Alt, "B")).Clicked() {
			app.HidePanel(p)
		}
	})
}

func (app *App) panelTab(c *ui.Context, p *panelState, i int, s *PanelSurface) {
	active := i == p.Active
	label, symbol := app.surfaceLabel(p, s)
	tab := ui.ButtonBase(c).Row().AlignItems(ui.Center).Gap(6).Height(28).MinWidth(100).MaxWidth(176).PaddingX(8).Radius(6).Label(label)
	if active {
		tab.Background(widgets.Chosen)
	} else if tab.Hovered() {
		tab.Background(widgets.Hover)
	}
	tab.Children(func() {
		widgets.Icon(c, symbol, 13, widgets.Muted)
		color := widgets.Muted
		if active {
			color = widgets.Ink
		}
		ui.Text(c, label).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12).TextColor(color)
		if p.Dirty(s) {
			ui.Box(c.Key("dirty")).Size(7, 7).Radius(4).Shrink(0).Background(ui.Hex("#e0a030")).Tooltip(tr("Unsaved changes. Press ⌘S to save."))
		}
		x := ui.ButtonBase(c.Key("close")).Size(16, 16).Shrink(0).Radius(4).Justify(ui.Center).AlignItems(ui.Center).Label(tr("Close") + label)
		if x.Hovered() {
			x.Background(widgets.Chosen)
		}
		x.Children(func() { widgets.Icon(c, "x", 10, widgets.Muted) })
		x.OnClick(func() { app.closeSurface(p, i) })
	})
	if tab.Clicked() {
		p.Active = i
	}
}

func (app *App) panelChooser(c *ui.Context, p *panelState) {
	ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Center).Justify(ui.Center).Padding(0, 20, 32, 20).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(420).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Open a panel")).FontSize(13).FontWeight(500)
			ui.Text(c, tr("Choose what to show in the right panel")).FontSize(12).TextColor(widgets.Muted).Margin(5, 0, 0, 0)
			for row, kinds := range [][]surfaceKind{{surfaceBrowser, SurfaceTerminal}, {SurfaceFiles, surfaceDiff}, {SurfaceRuntime}} {
				ui.Row(c.Key(row)).FillWidth().Gap(8).Margin(map[bool]float32{true: 18, false: 8}[row == 0], 0, 0, 0).Children(func() {
					for _, k := range kinds {
						app.surfaceCard(c.Key(k), p, k)
					}
				})
			}
		})
	})
}

func (app *App) surfaceCard(c *ui.Context, p *panelState, k surfaceKind) {
	card := ui.ButtonBase(c).Column().AlignItems(ui.Start).Height(112).Grow(1).Basis(0).MinWidth(0).Padding(14).Radius(8).
		Border(1, widgets.Border).Background(widgets.Panel).Label(k.label())
	if card.Hovered() {
		card.Background(widgets.Canvas).BorderColor(widgets.Muted)
	}
	card.Children(func() {
		widgets.Icon(c, k.icon(), 18, widgets.Muted)
		ui.Text(c, k.label()).FontSize(12).FontWeight(500).Margin(12, 0, 0, 0)
		ui.Text(c, k.description()).FontSize(12).TextColor(widgets.Muted).MaxLines(2).Margin(4, 0, 0, 0)
	})
	if card.Clicked() {
		app.openSurface(p, k)
	}
}

func panelMessage(c *ui.Context, title, detail string) {
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(6).Padding(24).Children(func() {
		ui.Text(c, title).FontWeight(500)
		if detail != "" {
			ui.Text(c, detail).FontSize(12).TextColor(widgets.Muted).TextAlign(ui.Center)
		}
	})
}

func panelBar(c *ui.Context, fn func()) {
	ui.Row(c).Height(38).Shrink(0).Padding(0, 10).Gap(4).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(widgets.Line).Children(fn)
}

func (app *App) startBrowser(s *PanelSurface) {
	w := uiruntime.CurrentWindow()
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

func (app *App) navigate(s *PanelSurface) {
	target := BrowserURL(s.address)
	if target == "" || s.web == nil {
		return
	}
	s.address, s.loaded = target, target
	if app.C.RemoteHost() {
		app.navigateRemoteBrowser(s, target, webservice.Service{})
		return
	}
	if err := s.web.Page().LoadURL(target); err != nil {
		s.webErr = err.Error()
	}
}

func (app *App) browserView(c *ui.Context, s *PanelSurface) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		panelBar(c, func() {
			if s.web != nil {
				if widgets.ToolButton(c.Key("back"), "arrow-left", tr("Back")).Clicked() {
					s.web.Page().GoBack()
				}
				if widgets.ToolButton(c.Key("forward"), "arrow-right", tr("Forward")).Clicked() {
					s.web.Page().GoForward()
				}
				if widgets.ToolButton(c.Key("reload"), "refresh", tr("Reload")).Clicked() {
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

func (app *App) startTerminal(s *PanelSurface, dir string) {
	s.Starting, s.termErr = true, ""
	files := app.C.WorkspaceFiles()
	uiruntime.GoAsync(func() {
		opts := terminal.Options{
			Dir:                     dir,
			ShellIntegration:        true,
			CopyOnSelect:            terminal.CopyOnSelectClipboard,
			SelectOnDrag:            true,
			SelectOnDragInAltScreen: true,
		}
		var err error
		opts.Conn, err = files.Terminal(dir)
		var t *terminal.Terminal
		if err == nil {
			t, err = terminal.New(opts)
		}
		if err != nil && opts.Conn != nil {
			opts.Conn.Close()
		}
		app.C.Dispatch(func() {
			s.Starting = false
			if err != nil {
				s.termErr = err.Error()
				return
			}
			if s.closed {
				go t.Close()
				return
			}
			s.Term = t
			go func() {
				<-t.Done()
				uiruntime.Invalidate()
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

func (app *App) terminalView(c *ui.Context, p *panelState, s *PanelSurface) {
	restart := func(title, detail string) {
		ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Padding(24).Children(func() {
			ui.Text(c, title).FontWeight(500)
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(widgets.Muted).TextAlign(ui.Center)
			}
			if ui.Button(c.Key("restart"), tr("Restart")).Clicked() {
				if s.Term != nil {
					go s.Term.Close()
					s.Term = nil
				}
				app.startTerminal(s, p.Cwd)
			}
		})
	}
	switch {
	case s.Starting:
		ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Spinner(c)
			widgets.MutedText(c, tr("Starting terminal…"))
		})
	case s.termErr != "":
		restart(tr("Terminal unavailable"), s.termErr)
	case s.Term == nil:
		restart(tr("Terminal unavailable"), tr("Close and reopen the terminal panel"))
	case terminalExited(s.Term):
		restart(tr("Terminal exited"), fmt.Sprintf(tr("Exit code %d"), s.Term.ExitCode()))
	default:
		ui.Box(c).Grow(1).MinHeight(0).Background(widgets.Canvas).Padding(6, 4, 4, 8).Children(func() {
			// Keep the terminal distinct from the loading state's children so
			// AutoFocus runs when the asynchronously created terminal appears.
			terminal.View(c.Key("terminal"), s.Term).Fill().AutoFocus()
		})
	}
}

type treeRow struct {
	entry projectfiles.FileEntry
	depth int
}

func (p *panelState) treeRows() []treeRow {
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

func (app *App) loadDir(p *panelState, rel string) {
	if p.loading[rel] {
		return
	}
	p.loading[rel] = true
	root := p.Cwd
	files := app.C.WorkspaceFiles()
	uiruntime.GoAsync(func() {
		var entries []projectfiles.FileEntry
		var err error
		entries, err = files.ReadDir(root, rel)
		app.C.Dispatch(func() {
			if p.Cwd != root {
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

func (app *App) openFile(p *panelState, rel string) {
	p.Selected = rel
	// An editor already open keeps its unsaved edits.
	if ed := p.Editors[rel]; ed != nil && ed.err == "" {
		return
	}
	kind := projectfiles.ImageFileKind(rel)
	ed := &fileEditor{loading: true, Preview: strings.EqualFold(path.Ext(rel), ".md") || kind != ""}
	p.Editors[rel] = ed
	root := p.Cwd
	files := app.C.WorkspaceFiles()
	uiruntime.GoAsync(func() {
		var text string
		var img ui.ImageSource
		var err error
		if kind == "bitmap" {
			var data []byte
			data, err = files.ReadImage(root, rel)
			if err == nil {
				if img, err = ui.DecodeImage(data); err != nil {
					err = fmt.Errorf(tr("Unable to decode image: %w"), err)
				}
			}
		} else {
			text, err = files.ReadText(root, rel)
			if err == nil && kind == "svg" {
				img = svgPreview(text)
			}
		}
		app.C.Dispatch(func() {
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
			ed.State = &editor.State{Buffer: buf, Label: rel}
		})
	})
}

func (app *App) saveFile(p *panelState, rel string, ed *fileEditor) {
	if ed.State == nil || !ed.State.Buffer.Modified() || ed.saving {
		return
	}
	buf := ed.State.Buffer
	if app.C.RemoteHost() {
		text, root, cl := buf.String(), p.Cwd, app.C.State().Remote
		ed.saving, ed.saveErr = true, ""
		uiruntime.GoAsync(func() {
			err := cl.Call("host", svcproto.HostRequest{Action: "write", Project: root, Path: rel, Text: text}, nil)
			app.C.Dispatch(func() {
				ed.saving = false
				if p.Cwd != root {
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
	err := fsutil.WriteFileAtomic(filepath.Join(p.Cwd, rel), func(f *os.File) error {
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

func (app *App) filesView(c *ui.Context, p *panelState) {
	if p.Selected != "" {
		app.fileEditorView(c.Key("file:"+p.Selected), p, p.Selected)
		return
	}
	rows := p.treeRows()
	p.tree.Key = func(i int) any { return rows[i].entry.Rel }
	p.tree.Label = func(i int) string { return rows[i].entry.Name }
	ui.List(c.Key("tree"), &p.tree, len(rows), func(i int) {
		app.treeRowView(c, p, rows[i])
	}).Grow(1).MinHeight(0).Padding(4, 8).Children(func() {
		switch {
		case p.dirErr[""] != "":
			panelMessage(c, tr("Unable to read workspace"), p.dirErr[""])
		case p.loading[""] && len(rows) == 0:
			ui.Row(c).Gap(8).Margin(ui.Auto).Children(func() {
				ui.Spinner(c)
				widgets.MutedText(c, tr("Loading files…"))
			})
		case len(rows) == 0:
			panelMessage(c, tr("This folder is empty"), p.Cwd)
		}
	})
}

func (app *App) treeRowView(c *ui.Context, p *panelState, r treeRow) {
	e := r.entry
	row := widgets.Quiet(c).FillWidth().Height(26).Gap(6).PaddingX(0).PaddingStart(6 + float32(r.depth)*14).Label(e.Name)
	row.Children(func() {
		if e.Dir {
			ch := widgets.Icon(c, "chevron-right", 12, widgets.Muted)
			ch.Rotate(ch.Animate("open", map[bool]float32{false: 0, true: 90}[p.expanded[e.Rel]], 120*time.Millisecond))
			widgets.Icon(c, "folder", 14, widgets.Faint)
		} else {
			ui.Box(c).Width(12).Shrink(0)
			widgets.Icon(c, "file", 14, widgets.Muted)
		}
		ui.Text(c, e.Name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12)
		if e.Dir && p.loading[e.Rel] {
			ui.Spinner(c).FontSize(11)
		}
	})
	if !row.Clicked() {
		return
	}
	if !e.Dir {
		app.openFile(p, e.Rel)
		return
	}
	p.expanded[e.Rel] = !p.expanded[e.Rel]
	if p.expanded[e.Rel] {
		app.loadDir(p, e.Rel)
	}
}

func (app *App) fileEditorView(c *ui.Context, p *panelState, rel string) {
	ed := p.Editors[rel]
	if ed == nil {
		p.Selected = ""
		return
	}
	markdownFile := strings.EqualFold(path.Ext(rel), ".md")
	imageKind := projectfiles.ImageFileKind(rel)
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		panelBar(c, func() {
			if widgets.ToolButton(c.Key("back"), "arrow-left", tr("Back to file list")).Clicked() {
				p.Selected = ""
			}
			ui.Text(c, rel).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12).TextColor(widgets.Muted)
			if (markdownFile || imageKind == "svg") && ed.State != nil {
				label, symbol := tr("Preview"), "eye"
				if ed.Preview {
					label, symbol = tr("Source"), "code"
				}
				if widgets.ToolButton(c.Key("preview"), symbol, label).Clicked() {
					ed.Preview = !ed.Preview
					if ed.Preview && imageKind == "svg" {
						ed.image = svgPreview(ed.State.Buffer.String())
					}
				}
			}
			if ed.State != nil && ed.State.Buffer.Modified() {
				if widgets.ToolButton(c.Key("save"), "device-floppy", tr("Save (⌘S)")).Disabled(ed.saving).Clicked() {
					app.saveFile(p, rel, ed)
				}
			}
		})
		if ed.saveErr != "" {
			ui.Text(c, ed.saveErr).FontSize(11).TextColor(widgets.Danger).Padding(4, 10)
		}
		if ed.saving {
			ui.Text(c, tr("Saving…")).FontSize(11).TextColor(widgets.Muted).Padding(4, 10)
		}
		switch {
		case ed.loading:
			ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Spinner(c)
				widgets.MutedText(c, tr("Loading files…"))
			})
		case ed.err != "":
			panelMessage(c, tr("Unable to open this file"), ed.err)
		case ed.Preview && imageKind != "" && ed.image == nil:
			panelMessage(c, tr("Unable to preview this image"), tr("Invalid SVG content or missing dimensions. Switch to source to view it."))
		case ed.Preview && imageKind != "":
			w, h := ui.ImageSize(ed.image)
			ui.Column(c.Key("image")).Grow(1).MinHeight(0).Padding(16).Gap(8).Background(widgets.Canvas).Children(func() {
				ui.Column(c).Grow(1).MinHeight(0).Children(func() {
					ui.Image(c, ed.image).Fill().Fit(ui.ScaleDown).Label(path.Base(rel))
				})
				ui.Text(c, fmt.Sprintf("%d × %d", int(w+0.5), int(h+0.5))).FontSize(11).TextColor(widgets.Muted).TextAlign(ui.Center).FillWidth()
			})
		case ed.Preview && ed.State != nil:
			ui.Scroll(c.Key("preview")).Grow(1).MinHeight(0).Background(widgets.Canvas).Children(func() {
				ui.Column(c).Padding(16, 20).Children(func() {
					markdown.View(c, ed.State.Buffer.String(), &app.Md)
				})
			})
		default:
			editor.View(c.Key("editor"), ed.State).Grow(1).FillWidth().Background(widgets.Canvas).
				OnShortcut(ui.Cmd, ui.KeyS, func() { app.saveFile(p, rel, ed) })
		}
	})
}

func (app *App) refreshDiff(p *panelState) {
	p.diffGen++
	gen, root, src := p.diffGen, p.Cwd, p.source
	previous := p.DiffViewPath
	p.diffBusy = true
	parent := app.C.State().Ctx
	if parent == nil {
		parent = context.Background()
	}
	files := app.C.WorkspaceFiles()
	uiruntime.GoAsync(func() {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		var snap projectgit.DiffSnapshot
		var err error
		snap, err = files.Diff(ctx, root, src)
		app.C.Dispatch(func() {
			if p.diffGen != gen || p.Cwd != root {
				return
			}
			p.diffBusy = false
			if err != nil {
				if p.Diff == nil {
					p.DiffErr = err.Error()
				} else {
					p.DiffErr = tr("Refresh failed: ") + err.Error()
				}
				return
			}
			p.DiffErr = ""
			p.Diff = &snap
			p.diffFile = 0
			for i, f := range snap.Files {
				if f.Path == previous {
					p.diffFile = i
				}
			}
			p.DiffView, p.DiffViewPath, p.sideNote = nil, "", ""
			if len(snap.Files) > 0 {
				app.loadDiffFile(p, p.diffFile)
			}
		})
	})
}

func (app *App) loadDiffFile(p *panelState, i int) {
	if p.Diff == nil || i < 0 || i >= len(p.Diff.Files) {
		return
	}
	p.diffFile = i
	f, snap, gen, root := p.Diff.Files[i], *p.Diff, p.diffGen, p.Cwd
	p.DiffViewPath = f.Path
	if f.Binary {
		p.DiffView, p.sideNote = nil, tr("Binary file; no diff available")
		return
	}
	p.sideNote = ""
	parent := app.C.State().Ctx
	if parent == nil {
		parent = context.Background()
	}
	files := app.C.WorkspaceFiles()
	uiruntime.GoAsync(func() {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		var old, new string
		var err error
		old, new, err = files.Sides(ctx, root, snap, f)
		app.C.Dispatch(func() {
			if p.diffGen != gen || p.DiffViewPath != f.Path {
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
			p.DiffView = &editor.DiffState{Old: before, New: after, Layout: editor.DiffUnified}
		})
	})
}

func (app *App) diffSurface(c *ui.Context, p *panelState) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		panelBar(c, func() {
			// The source, not its name, is the selection: names change with the
			// interface language.
			names := projectgit.DiffSourceNames()
			p.sourceName = names[p.source]
			if ui.Select(c.Key("source"), &p.sourceName, names).Label(tr("Review source")).FontSize(12).Changed() {
				for i, name := range names {
					if name == p.sourceName {
						p.source = projectgit.DiffSource(i)
					}
				}
				p.Diff, p.DiffView, p.DiffViewPath, p.DiffErr = nil, nil, "", ""
				app.refreshDiff(p)
			}
			ui.Spacer(c)
			if p.Diff != nil {
				ui.Text(c, fmt.Sprintf(tr("%d files"), len(p.Diff.Files))).FontSize(11).TextColor(widgets.Muted)
				ui.Text(c, fmt.Sprintf("+%d", p.Diff.Added)).FontSize(11).TextColor(widgets.Success)
				ui.Text(c, fmt.Sprintf("−%d", p.Diff.Deleted)).FontSize(11).TextColor(widgets.Danger)
			}
			if p.diffBusy {
				ui.Spinner(c).FontSize(12)
			}
			if widgets.ToolButton(c.Key("refresh"), "refresh", tr("Refresh")).Clicked() {
				app.refreshDiff(p)
			}
		})
		if p.DiffErr != "" && p.Diff != nil {
			ui.Text(c, p.DiffErr).FontSize(11).TextColor(widgets.Danger).Padding(4, 10)
		}
		switch {
		case p.Diff == nil && p.DiffErr != "":
			panelMessage(c, tr("Unable to read changes"), p.DiffErr)
			return
		case p.Diff == nil:
			ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Spinner(c)
				widgets.MutedText(c, tr("Loading changes…"))
			})
			return
		case len(p.Diff.Files) == 0:
			panelMessage(c, tr("No changes"), projectgit.DiffSourceNames()[p.Diff.Source]+tr(" is empty"))
			return
		}
		ui.Scroll(c.Key("changed-files")).MaxHeight(180).Shrink(0).Children(func() {
			ui.Column(c).Padding(4, 8).Gap(1).Children(func() {
				for i, f := range p.Diff.Files {
					app.changedFileRow(c.Key(f.Path), p, i, f)
				}
			})
		})
		ui.Box(c).Height(1).Shrink(0).Background(widgets.Line)
		switch {
		case p.sideNote != "":
			panelMessage(c, path.Base(p.DiffViewPath), p.sideNote)
		case p.DiffView != nil:
			editor.DiffView(c.Key("diff:"+p.DiffViewPath), p.DiffView).Grow(1).FillWidth().Background(widgets.Canvas)
		default:
			ui.Row(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Children(func() { ui.Spinner(c) })
		}
	})
}

func (app *App) changedFileRow(c *ui.Context, p *panelState, i int, f projectgit.ChangedFile) {
	row := widgets.Quiet(c).FillWidth().Height(26).Gap(6).PaddingX(6).Label(f.Path)
	if i == p.diffFile {
		row.Background(widgets.Chosen)
	}
	row.Children(func() {
		widgets.Icon(c, "file", 13, widgets.Muted)
		dir, name := path.Split(f.Path)
		ui.Text(c, name).Shrink(0).FontSize(12)
		ui.Text(c, strings.TrimSuffix(dir, "/")).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(11).TextColor(widgets.Muted)
		switch {
		case f.Binary:
			ui.Text(c, tr("Binary")).FontSize(11).TextColor(widgets.Muted)
		default:
			if f.Untracked {
				ui.Text(c, tr("New file")).FontSize(11).TextColor(widgets.Muted)
			}
			ui.Text(c, fmt.Sprintf("+%d", f.Added)).FontSize(11).TextColor(widgets.Success)
			ui.Text(c, fmt.Sprintf("−%d", f.Deleted)).FontSize(11).TextColor(widgets.Danger)
		}
	})
	if row.Clicked() && i != p.diffFile {
		app.loadDiffFile(p, i)
	}
}

// browserURL turns what is typed in the address bar into a URL: a port is a
// local server, a host without a scheme is https, local hosts http.
func BrowserURL(input string) string {
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
