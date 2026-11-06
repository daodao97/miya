package controller

import (
	context "context"
	appmodel "miya/internal/appmodel"
	async "miya/internal/async"
	rt "miya/internal/runtime"
	webservice "miya/internal/webservice"
	sort "sort"
	strconv "strconv"
	sync "sync"
)

// Project overview aggregates proven session resources first. Directory-based
// listeners remain explicitly unowned candidates, never session evidence.
func (c *Controller) ProjectRuntimeServices(ctx context.Context, project string) ([]webservice.Service, error) {
	type plan struct {
		manager *appmodel.RuntimeManager
		keys    []rt.SessionKey
	}
	prepared, ok := async.OnMain(ctx, c.Dispatch, func() plan {
		manager := c.Runtimes()
		out := plan{manager: manager}
		for _, thread := range c.projectThreads(project) {
			if thread.ServerID == "" {
				continue
			}
			key := appmodel.RuntimeKey(c.ThreadRef(thread))
			if manager.Store().Read(key).Epoch == "" && !thread.Busy && c.ClaudeRuns[thread.ID] == nil {
				continue
			}
			manager.Register(key, BackendOf(thread).Runtime(c, thread))
			out.keys = append(out.keys, key)
		}
		return out
	})
	if !ok {
		return nil, ctx.Err()
	}
	views := make([]rt.View, len(prepared.keys))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(4, len(views)) {
		workers.Go(func() {
			for i := range jobs {
				views[i] = prepared.manager.Refresh(ctx, prepared.keys[i])
			}
		})
	}
	for i := range views {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	services := []webservice.Service{}
	owned := map[string]bool{}
	for i, view := range views {
		for _, service := range appmodel.RuntimeResult(prepared.manager, prepared.keys[i], view).Services {
			services = append(services, service)
			owned[service.Host+":"+strconv.Itoa(service.Port)] = true
		}
	}
	candidates, err := webservice.Discover(ctx, project)
	if err != nil && len(services) == 0 {
		return nil, err
	}
	for _, service := range candidates {
		if !owned[service.Host+":"+strconv.Itoa(service.Port)] {
			services = append(services, service)
		}
	}
	sort.SliceStable(services, func(i, j int) bool { return services[i].Port < services[j].Port })
	return services, nil
}
