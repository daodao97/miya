package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"miya/internal/selfupdate"
)

// UpdateBackend separates installation from the service's task and shutdown
// lifecycle. All lifecycle decisions still run on the service executor.
type UpdateBackend interface {
	Status() selfupdate.Status
	Busy() bool
	StartCheck(context.Context, func(error)) error
	StartInstall(context.Context, string, func(error)) error
	ReopenWindows(context.Context) error
	ReopenFailed(error)
}

func (s *Service) handleUpdate(args json.RawMessage) (any, error) {
	if s.Updates == nil {
		return nil, errors.New("Update Miya on the target computer once to enable remote updates")
	}
	var r struct{ Action, Version string }
	if err := json.Unmarshal(args, &r); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	done := func(err error) {
		cancel()
		s.exec.Push(func() { s.MarkDirty() })
	}
	switch r.Action {
	case "status":
		cancel()
	case "check":
		if err := s.Updates.StartCheck(ctx, done); err != nil {
			cancel()
			return nil, err
		}
	case "install":
		if s.updateWorking {
			cancel()
			return nil, errors.New("A Miya update is already in progress")
		}
		s.updateWorking = true
		if err := s.Updates.StartInstall(ctx, r.Version, func(err error) {
			if err == nil {
				// Do not stop the service if reopening the windows failed. The
				// installed release remains available for a normal local launch.
				reopenCtx, reopenCancel := context.WithTimeout(context.Background(), 20*time.Second)
				err = s.Updates.ReopenWindows(reopenCtx)
				reopenCancel()
				if err != nil {
					s.Updates.ReopenFailed(err)
				}
			}
			s.exec.Push(func() {
				s.updateWorking = false
				if err == nil {
					s.QuitWhenIdle = true
					s.host.ServicePause()
				}
				s.MarkDirty()
			})
			done(err)
		}); err != nil {
			s.updateWorking = false
			cancel()
			return nil, err
		}
	default:
		cancel()
		return nil, errors.New("Unknown Miya update action")
	}
	return s.Updates.Status(), nil
}
