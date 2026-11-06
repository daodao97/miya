package controller

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"miya/internal/async"
	"miya/internal/desktop/control"
	"miya/internal/model"
	"miya/internal/phone"
	"miya/internal/runtime/tracker"
	"miya/internal/webservice"
)

func (c *Controller) FileServer() http.Handler {
	files := phone.DesktopFileServer{Runtime: c.ReadRuntime, WebServices: c.ProjectRuntimeServices, Projects: func(ctx context.Context) ([]phone.FileProject, error) {
		reply := make(chan []phone.FileProject, 1)
		c.Dispatch(func() {
			projects := append([]model.Project(nil), c.Projects()...)
			found := false
			for _, p := range projects {
				if p.Path == c.Cwd {
					found = true
				}
			}
			if !found && c.Cwd != "" && filepath.Dir(c.Cwd) != c.Cwd {
				projects = append(projects, model.Project{Name: filepath.Base(c.Cwd), Path: c.Cwd})
			}
			var out []phone.FileProject
			for _, p := range projects {
				if p.Path != "" {
					out = append(out, phone.FileProject{ID: phone.ProjectFileID(p.Path), Name: p.Name, Path: p.Path})
				}
			}
			reply <- out
		})
		select {
		case roots := <-reply:
			return roots, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.Ctx.Done():
			return nil, c.Ctx.Err()
		}
	}}
	web := webservice.Server(func(ctx context.Context, token, host string, port int) error {
		manager, ok := async.OnMain(ctx, c.Dispatch, func() *tracker.Manager { return c.Runtimes() })
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
