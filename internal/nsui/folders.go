package nsui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui/filepicker"

	"miya/internal/nswidgets"
	svcproto "miya/internal/service/protocol"
)

// folderPicker chooses a folder of the workspace: on this computer, or on
// the service a mirrored window shows, as MyGo's project dialog browses
// the server's folders. browse opens it at start, the open folder when
// empty.
func folderPicker(cx *core.Scope, c Host, open *core.Signal[bool], choose func(path string)) (node core.Node, browse func(start string)) {
	state := filepicker.NewState(cx, filepicker.List, false, 0)
	state.Load = func(path string, done func(filepicker.Folder, error)) {
		if path == "" {
			path = c.State().Cwd
		}
		list := localFolder
		if c.RemoteHost() {
			cl := c.State().Remote
			list = func(path string) ([]filepicker.Entry, error) {
				var entries []svcproto.HostEntry
				if err := cl.Call("host", svcproto.HostRequest{Action: "browse", Path: path}, &entries); err != nil {
					return nil, err
				}
				out := make([]filepicker.Entry, 0, len(entries))
				for _, e := range entries {
					// The service lists folders only, which is all a project needs.
					out = append(out, filepicker.Entry{Name: e.Name, Path: e.Path, Dir: true, Hidden: strings.HasPrefix(e.Name, ".")})
				}
				return out, nil
			}
		}
		go func() {
			entries, err := list(path)
			folder := filepicker.Folder{Path: path, Entries: entries}
			if parent := filepath.Dir(path); parent != path {
				folder.Parent = parent
			}
			cx.Post(func() { done(folder, err) })
		}()
	}
	places := func() []filepicker.Section {
		var list []filepicker.Place
		if !c.RemoteHost() {
			if home, err := os.UserHomeDir(); err == nil {
				list = append(list, filepicker.Place{Name: tr("Home"), Path: home, Icon: nswidgets.IconOf("user-circle")})
			}
		}
		if cwd := c.State().Cwd; cwd != "" {
			list = append(list, filepicker.Place{Name: filepath.Base(cwd), Path: cwd, Icon: nswidgets.IconOf("folder")})
		}
		return []filepicker.Section{{Places: list}}
	}
	node = filepicker.Dialog{State: state, Open: open.Get, OnOpen: open.Set, SectionsFunc: places, Choose: choose, TestID: "folder-picker",
		Labels: filepicker.Labels{Title: tr("Select project directory"), Address: tr("Folder path"),
			Icons: tr("Icons"), List: tr("List"), Hidden: tr("Show hidden files"),
			Name: tr("Name"), Modified: tr("Modified"), Size: tr("Size"),
			Empty: tr("This folder is empty"), Cancel: tr("Cancel"), Choose: tr("Choose"), Close: tr("Close")}}
	browse = func(start string) {
		open.Set(true)
		state.Open(strings.TrimSpace(start))
	}
	return node, browse
}

// localFolder lists a folder of this computer, folders first.
func localFolder(path string) ([]filepicker.Entry, error) {
	dir, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]filepicker.Entry, 0, len(dir))
	for _, d := range dir {
		e := filepicker.Entry{Name: d.Name(), Path: filepath.Join(path, d.Name()), Dir: d.IsDir(), Hidden: strings.HasPrefix(d.Name(), ".")}
		if info, err := d.Info(); err == nil {
			e.Modified, e.Size = info.ModTime(), info.Size()
			if info.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Stat(e.Path); err == nil {
					e.Dir = target.IsDir()
				}
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
