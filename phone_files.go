package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"golang.org/x/text/language"
	"miya/internal/async"
	"miya/internal/desktop/control"
	"miya/internal/model"
	"miya/internal/webservice"
	"miya/locales"
)

func projectFileID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:16])
}

type fileProject struct{ ID, Name, Path string }
type desktopFileServer struct {
	Projects    func(context.Context) ([]fileProject, error)
	WebServices func(context.Context, string) ([]webservice.Service, error)
	Runtime     func(context.Context, ThreadRef, string) (conversationRuntime, error)
}

func (c *chatController) fileServer() http.Handler {
	files := desktopFileServer{Runtime: c.readRuntime, WebServices: c.projectRuntimeServices, Projects: func(ctx context.Context) ([]fileProject, error) {
		reply := make(chan []fileProject, 1)
		c.dispatch(func() {
			projects := append([]Project(nil), c.projects()...)
			found := false
			for _, p := range projects {
				if p.Path == c.cwd {
					found = true
				}
			}
			if !found && c.cwd != "" && filepath.Dir(c.cwd) != c.cwd {
				projects = append(projects, Project{Name: filepath.Base(c.cwd), Path: c.cwd})
			}
			var out []fileProject
			for _, p := range projects {
				if p.Path != "" {
					out = append(out, fileProject{ID: projectFileID(p.Path), Name: p.Name, Path: p.Path})
				}
			}
			reply <- out
		})
		select {
		case roots := <-reply:
			return roots, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.ctx.Done():
			return nil, c.ctx.Err()
		}
	}}
	web := webservice.Server(func(ctx context.Context, token, host string, port int) error {
		manager, ok := async.OnMain(ctx, c.dispatch, func() *runtimeManager { return c.runtimes() })
		if !ok {
			return ctx.Err()
		}
		return manager.ValidateToken(ctx, token, host, port)
	})
	controls := c.desktopControls()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, control.Prefix+"/") {
			controls.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, webservice.Prefix) {
			web.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func (s desktopFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Only viewing and downloading are supported", http.StatusMethodNotAllowed)
		return
	}
	// Do not normalize traversal into a different file or accept OS separators.
	for _, part := range strings.Split(r.URL.Path, "/") {
		if part == ".." || strings.ContainsAny(part, "\\\x00") {
			http.Error(w, "Invalid path", 400)
			return
		}
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	timeout := 5 * time.Second
	if parts[1] == webservice.ProjectFile || parts[1] == ".miya-runtime" {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	projects, err := s.Projects(ctx)
	if err != nil {
		http.Error(w, "Desktop is not responding. Try again later.", 503)
		return
	}
	var project fileProject
	for _, p := range projects {
		if p.ID == parts[0] {
			project = p
			break
		}
	}
	if project.Path == "" {
		http.NotFound(w, r)
		return
	}
	if parts[1] == ".miya-runtime" {
		if s.Runtime == nil {
			http.NotFound(w, r)
			return
		}
		ref, ok := model.ParseThreadURL(r.URL.Query().Get("ref"))
		if !ok {
			http.Error(w, "Invalid conversation identity", 400)
			return
		}
		result, err := s.Runtime(ctx, ref, project.Path)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != "HEAD" {
			_ = json.NewEncoder(w).Encode(result)
		}
		return
	}
	if parts[1] == webservice.ProjectFile {
		discover := s.WebServices
		if discover == nil {
			discover = webservice.Discover
		}
		services, err := discover(ctx, project.Path)
		if err != nil {
			http.Error(w, "Unable to find project web services. Refresh or enter a port manually.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != "HEAD" {
			_ = json.NewEncoder(w).Encode(services)
		}
		return
	}
	root, err := os.OpenRoot(project.Path)
	if err != nil {
		http.Error(w, "Project directory unavailable", 404)
		return
	}
	defer root.Close()
	if parts[1] == ".miya-references" {
		files, err := indexProjectFiles(ctx, project.Path)
		if err != nil {
			http.Error(w, "Unable to search project files. Try again later.", http.StatusServiceUnavailable)
			return
		}
		// Keep suggestions inside the selected project, including when rg
		// reports a symlink or a file disappears during indexing.
		valid := files[:0]
		for _, name := range files {
			if ctx.Err() != nil {
				http.Error(w, "File search timed out", http.StatusServiceUnavailable)
				return
			}
			if info, err := root.Stat(name); err == nil && info.Mode().IsRegular() {
				valid = append(valid, name)
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != "HEAD" {
			_ = json.NewEncoder(w).Encode(fileChoices(valid, r.URL.Query().Get("q")))
		}
		return
	}
	name := strings.TrimSuffix(parts[1], "/")
	if name == "" {
		name = "."
	}
	before, err := root.Stat(name)
	if err != nil || !before.IsDir() && !before.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	file, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			u := *r.URL
			u.Path += "/"
			http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
			return
		}
		entries, err := file.ReadDir(-1)
		if err != nil {
			http.Error(w, "Unable to read directory", 403)
			return
		}
		s.directory(w, r, project, root, name, entries)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "Unable to preview this file", 403)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	}
	// Source and Markdown remain readable; HTML keeps its browser MIME type.
	ext := strings.ToLower(filepath.Ext(info.Name()))
	if plainFileExtension(ext) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func plainFileExtension(ext string) bool {
	switch ext {
	case ".go", ".md", ".markdown", ".txt", ".yaml", ".yml", ".toml", ".ini", ".log", ".sh", ".py", ".ts", ".tsx", ".jsx", ".rs", ".sql", ".csv", ".mod", ".sum":
		return true
	}
	return false
}

type phoneFileEntry struct {
	Name, URL, Download, Size string
	Dir                       bool
}
type fileDirectory struct {
	Project, Path, Parent                                        string
	Language, FilesLabel, ParentLabel, DownloadLabel, EmptyLabel string
	Entries                                                      []phoneFileEntry
}

func (s desktopFileServer) directory(w http.ResponseWriter, r *http.Request, project fileProject, root *os.Root, name string, entries []fs.DirEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	preferred, _, _ := language.ParseAcceptLanguage(r.Header.Get("Accept-Language"))
	tags := make([]string, len(preferred))
	for i, tag := range preferred {
		tags[i] = tag.String()
	}
	lang := i18n.Match(tags, locales.Catalog.Languages())
	if lang == "" {
		lang = locales.Catalog.Source()
	}
	text := func(source string) string { return locales.TextIn(lang, source) }
	data := fileDirectory{Project: project.Name, Path: "/", Language: lang, FilesLabel: text("Files"), ParentLabel: text("Parent folder"), DownloadLabel: text("Download"), EmptyLabel: text("This folder is empty")}
	if name != "." {
		data.Path = "/" + name
	}
	if name != "." {
		data.Parent = "../"
	}
	for _, entry := range entries {
		relative := entry.Name()
		if name != "." {
			relative = path.Join(name, relative)
		}
		// os.Root also excludes symlinks that escape the selected project.
		info, err := root.Stat(relative)
		if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		item := phoneFileEntry{Name: entry.Name(), Dir: info.IsDir(), Size: fileSize(info.Size())}
		item.URL = url.PathEscape(entry.Name())
		if item.Dir {
			item.URL += "/"
			item.Size = text("Directory")
		} else {
			item.Download = item.URL + "?download=1"
		}
		data.Entries = append(data.Entries, item)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	if r.Method == "HEAD" {
		return
	}
	_ = fileDirectoryTemplate.Execute(w, data)
}
func fileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1<<20 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
}

var fileDirectoryTemplate = template.Must(template.New("files").Parse("<!doctype html><html lang=\"{{.Language}}\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><meta name=\"color-scheme\" content=\"light dark\"><title>{{.Project}} · {{.FilesLabel}}</title><style>body{font:17px -apple-system,system-ui;margin:0;padding:24px 16px;background:#f2f2f7;color:#171719}header{margin:8px 4px 24px}h1{font-size:28px;margin:0 0 8px}small{color:#777;overflow-wrap:anywhere}main{background:white;border-radius:14px;overflow:hidden}article{display:flex;align-items:center;border-bottom:1px solid #ddd;min-height:64px;padding:0 14px;gap:12px}article:last-child{border:0}a{color:#007aff;text-decoration:none}article>a:first-child{flex:1;min-width:0;padding:16px 0;overflow-wrap:anywhere}.download{font-size:14px;padding:16px 0;white-space:nowrap}p{padding:20px;color:#777}@media(prefers-color-scheme:dark){body{background:#000;color:#eee}main{background:#1c1c1e}article{border-color:#333}a{color:#0a84ff}}</style><header><h1>{{.Project}}</h1><small>{{.Path}}</small></header><main>{{if .Parent}}<article><a href=\"{{.Parent}}\">↰ {{.ParentLabel}}</a></article>{{end}}{{range .Entries}}<article><a href=\"{{.URL}}\">{{if .Dir}}📁{{else}}📄{{end}} {{.Name}}<br><small>{{.Size}}</small></a>{{if .Download}}<a class=\"download\" href=\"{{.Download}}\">{{$.DownloadLabel}}</a>{{end}}</article>{{else}}<p>{{.EmptyLabel}}</p>{{end}}</main></html>"))
