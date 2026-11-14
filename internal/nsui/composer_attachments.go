package nsui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/nswidgets"
)

func localPaths(uris []string) []string {
	var out []string
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err == nil && u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") {
			out = append(out, filepath.FromSlash(u.Path))
		} else if filepath.IsAbs(raw) {
			out = append(out, raw)
		}
	}
	return out
}
func (d *desktopComposer) addPaths(paths []string) {
	if len(paths) > 0 {
		d.host.AddAttachments(paths)
	}
}
func (d *desktopComposer) pickFiles() {
	project, thread, source := d.host.State().Cwd, d.host.Current().ID, d.host.State().ReferenceSource
	directory := project
	if d.host.RemoteHost() {
		directory, _ = os.UserHomeDir()
	}
	d.cx.PickFiles(core.PickFilesOptions{Multiple: true, Dialog: core.OpenDialogOptions{Title: tr("Add files"), Directory: directory, Prompt: tr("Attach")}}, func(files []core.File, err error) {
		if err != nil {
			d.notice.Set(err.Error())
			return
		}
		if len(files) == 0 {
			return
		}
		d.stageAttachments(project, thread, source, func() ([]string, error) {
			var paths []string
			for _, f := range files {
				if f.Path != "" {
					paths = append(paths, f.Path)
					continue
				}
				r, err := f.Open()
				if err != nil {
					return paths, err
				}
				p, err := stageAttachment(f.Name, r)
				_ = r.Close()
				if err != nil {
					return paths, err
				}
				paths = append(paths, p)
			}
			return paths, nil
		})
	})
}
func (d *desktopComposer) paste(e core.PasteEvent) {
	if d.host.Current().ReadOnly || d.host.Current().Loading {
		return
	}
	if paths := localPaths(e.URIs); len(paths) > 0 {
		d.addPaths(paths)
		return
	}
	if len(e.Files) == 0 {
		return
	}
	d.stageAttachments(d.host.State().Cwd, d.host.Current().ID, d.host.State().ReferenceSource, func() ([]string, error) {
		var paths []string
		for _, f := range e.Files {
			name := f.Name
			if name == "" {
				exts, _ := mime.ExtensionsByType(f.MIME)
				ext := ".bin"
				if len(exts) > 0 {
					ext = exts[0]
				}
				name = "clipboard" + ext
			}
			p, err := stageAttachment(name, bytes.NewReader(f.Data))
			if err != nil {
				return paths, err
			}
			paths = append(paths, p)
		}
		return paths, nil
	})
}

// The temporary directory preserves the original file name and extension. The
// controller may upload it after this scope closes, so it outlives the view.
func stageAttachment(name string, r io.Reader) (string, error) {
	dir, err := os.MkdirTemp("", "miya-attachment-")
	if err != nil {
		return "", err
	}
	name = filepath.Base(name)
	if name == "." || name == "/" || name == "" {
		name = "attachment"
	}
	p := filepath.Join(dir, name)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_, err = io.Copy(f, r)
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return p, nil
}
func (d *desktopComposer) stageAttachments(project string, thread int, source string, work func() ([]string, error)) {
	d.preparing.Update(func(n int) int { return n + 1 })
	go func() {
		paths, err := work()
		d.cx.Post(func() {
			d.preparing.Update(func(n int) int { return n - 1 })
			if d.host.State().Cwd != project || d.host.Current().ID != thread || d.host.State().ReferenceSource != source {
				d.notice.Set(tr("The connected workspace changed; retry from the original workspace"))
				return
			}
			if err != nil {
				d.notice.Set(err.Error())
			}
			d.addPaths(paths)
		})
	}()
}
func (d *desktopComposer) attachmentChips() core.Node {
	return core.View{TestID: "composer-attachments", Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 8}, Children: []core.Node{
		core.Each(Live(d.attachments), func(p string) string { return p }, func(cx *core.Scope, get func() string, _ func() int) core.Node {
			path := core.Untracked(cx, get)
			name := filepath.Base(path)
			var preview []core.Node
			if strings.HasPrefix(mime.TypeByExtension(filepath.Ext(path)), "image/") {
				// Newly picked/pasted files belong to this desktop even when
				// the conversation is mirrored. Only server markers fetch remotely.
				src := func() string { return (&url.URL{Scheme: "file", Path: path}).String() }
				if strings.HasPrefix(path, "miya-server:") {
					src = remoteImageSource(cx, d.host, path)
				}
				preview = append(preview, core.Image{SourceFunc: func() core.ImageSource { return core.Src(src()) }, ResizeMode: core.Cover, Label: tr("Image preview"), TestID: "attachment-image-" + path, Style: core.Style{Width: core.Pt(100), Height: core.Pt(75), CornerRadius: 6}})
			}
			preview = append(preview, core.Text{Content: name, MaxLines: 1, Style: core.Style{FontSize: 11, MaxWidth: core.Pt(180)}}, ui.IconButton{TestID: "remove-attachment-" + path, Label: tr("Remove attachment ") + name, Icon: nswidgets.IconOf("x"), Size: 24, OnPress: func() { d.host.RemoveAttachment(path) }})
			return core.View{TestID: "attachment-" + path, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4}, Children: preview}
		})}}
}
func imageDataURI(data []byte) string {
	return "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
}
