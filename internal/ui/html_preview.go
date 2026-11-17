package desktopui

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	phoneproxy "miya/internal/phone/proxy"
	svcproto "miya/internal/service/protocol"
)

func isHTMLFile(rel string) bool {
	ext := path.Ext(rel)
	return strings.EqualFold(ext, ".html") || strings.EqualFold(ext, ".htm")
}

func localHTMLPreviewPath(root, rel string) (string, string) {
	if filepath.IsLocal(rel) {
		return root, filepath.ToSlash(filepath.Clean(rel))
	}
	// Message links can open a file outside the workspace. Give that file
	// its own directory as the asset root: browsers collapse ../ in URLs.
	file := rel
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	return filepath.Dir(file), filepath.Base(file)
}

func (ed *fileEditor) closeHTMLPreview() {
	if ed.html != nil {
		closeSurfaceResources(ed.html)
		ed.html = nil
	}
}

func (p *panelState) closeFilePreviews() {
	for _, ed := range p.Editors {
		ed.closeHTMLPreview()
	}
}

func (app *App) startHTMLPreview(p *panelState, rel string, ed *fileEditor) *PanelSurface {
	s := &PanelSurface{}
	w := mainWindow()
	if w == nil {
		s.webErr = tr("The browser requires an app window")
		return s
	}
	web, err := w.NewWebView(mygo.WebViewOptions{
		BackgroundColor: "#ffffff",
		Page:            mygo.PageOptions{Untrusted: true},
	})
	if err != nil {
		s.webErr = err.Error()
		return s
	}
	s.web = web
	var proxy *phoneproxy.PhoneFileProxy
	if app.C.RemoteHost() {
		cl := app.C.State().Remote
		if cl == nil || cl.Wire() == nil {
			s.webErr = svcproto.ErrClosed.Error()
			return s
		}
		proxy, err = phoneproxy.NewPhoneFileProxy(cl.Wire(), p.Cwd)
	} else {
		var root string
		root, rel = localHTMLPreviewPath(p.Cwd, rel)
		proxy, err = phoneproxy.NewLocalFileProxy(root)
	}
	if err != nil {
		s.webErr = err.Error()
		return s
	}
	s.webProxy = proxy
	proxy.SetPreview(rel, ed.State.Buffer.String())
	web.Page().OnDidFailLoad(func(err *mygo.LoadError) {
		app.C.Dispatch(func() {
			if !s.closed {
				s.webErr = err.Description
			}
		})
	})
	// The bootstrap redirects before the first document commits, so an
	// untrusted page needs an explicit origin for that initial navigation.
	origin, _ := url.Parse(proxy.URL)
	web.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
		target, err := url.Parse(e.URL)
		if err != nil || target.Scheme != origin.Scheme || target.Host != origin.Host {
			e.PreventDefault()
		}
	})
	fileURL := (&url.URL{Path: "/" + rel}).String()
	if err := web.Page().LoadURL(proxy.URL + "?path=" + url.QueryEscape(fileURL)); err != nil {
		s.webErr = err.Error()
	}
	return s
}

func (app *App) htmlPreviewView(c *ui.Context, p *panelState, rel string, ed *fileEditor) {
	if ed.html == nil {
		ed.html = app.startHTMLPreview(p, rel, ed)
	}
	if ed.html.webErr != "" {
		panelMessage(c, tr("Browser unavailable"), ed.html.webErr)
		return
	}
	ui.WebView(c.Key("html-preview"), ed.html.web).Grow(1).MinHeight(0)
}
