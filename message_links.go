package main

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var fileLinkLocation = regexp.MustCompile(`:[0-9]+(?::[0-9]+)?$`)

// Message file links use the conversation's workspace, just as the agent does.
// Reading them in the files surface also preserves any unsaved editor content.
func (app *chatApp) openMessageLink(link string) {
	if rel, ok := localFileLink(app.c.cwd, link); ok {
		p := app.panel()
		app.openSurface(p, surfaceFiles)
		app.openFile(p, rel)
		return
	}
	if ui.SafeToOpen(link) {
		go mygo.Shell.OpenExternal(link)
	}
}

func localFileLink(cwd, link string) (string, bool) {
	link = strings.TrimSpace(link)
	if link == "" || strings.HasPrefix(link, "#") || strings.HasPrefix(link, "//") || strings.ContainsAny(link, "\\\x00\r\n") {
		return "", false
	}
	// Agent source citations may append :line or :line:column to the path.
	u, err := url.Parse(fileLinkLocation.ReplaceAllString(link, ""))
	if err != nil || u.Opaque != "" || u.Host != "" && !(strings.EqualFold(u.Scheme, "file") && strings.EqualFold(u.Host, "localhost")) {
		return "", false
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "file") {
		return "", false
	}
	file := u.Path
	if file == "" || strings.ContainsAny(file, "\\\x00\r\n") {
		return "", false
	}
	if !filepath.IsAbs(file) {
		if u.Scheme != "" {
			return "", false
		}
		file = filepath.Join(cwd, file)
	}
	rel, err := filepath.Rel(cwd, file)
	return rel, err == nil
}
