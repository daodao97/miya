package controller

import (
	context "context"
	appmodel "miya/internal/appmodel"
	async "miya/internal/async"
	control "miya/internal/desktop/control"
	webservice "miya/internal/webservice"
	http "net/http"
	filepath "path/filepath"
	strings "strings"
)

func (c *Controller) FileServer() http.Handler {
	files := appmodel.DesktopFileServer{Runtime: c.ReadRuntime, WebServices: c.ProjectRuntimeServices, Projects: func(ctx context.Context) ([]appmodel.FileProject, error) {
		reply := make(chan []appmodel.FileProject, 1)
		c.Dispatch(func() {
			projects := append([]appmodel.Project(nil), c.Projects()...)
			found := false
			for _, p := range projects {
				if p.Path == c.Cwd {
					found = true
				}
			}
			if !found && c.Cwd != "" && filepath.Dir(c.Cwd) != c.Cwd {
				projects = append(projects, appmodel.Project{Name: filepath.Base(c.Cwd), Path: c.Cwd})
			}
			var out []appmodel.FileProject
			for _, p := range projects {
				if p.Path != "" {
					out = append(out, appmodel.FileProject{ID: appmodel.ProjectFileID(p.Path), Name: p.Name, Path: p.Path})
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
		manager, ok := async.OnMain(ctx, c.Dispatch, func() *appmodel.RuntimeManager { return c.Runtimes() })
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
