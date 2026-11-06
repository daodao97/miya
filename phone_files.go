package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"miya/internal/async"
	"miya/internal/desktop/control"
	"miya/internal/webservice"
)

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
