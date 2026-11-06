package quota

import (
	"context"
	"errors"
	"time"
)

type Provider interface {
	ID() string
	Read(context.Context) (Reading, error)
}

type Resetter interface {
	Provider
	Reset(context.Context, ResetAttempt) (ResetResult, error)
}

// Service routes subscription operations without knowing provider protocols.
// Construct it with the integrations supported by the executable.
type Service struct{ providers []Provider }

func NewService(providers ...Provider) *Service {
	return &Service{providers: append([]Provider(nil), providers...)}
}

func (s *Service) Read(ctx context.Context, id string) Reading {
	r := Reading{Agent: id, At: time.Now()}
	for _, p := range s.providers {
		if p.ID() != id {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		var err error
		r, err = p.Read(ctx)
		if ctx.Err() != nil {
			err = errors.New("Read timed out. Try again later.")
		}
		r.Agent, r.At = id, time.Now()
		if err != nil {
			r.Error, r.Windows = err.Error(), nil
		}
		return r
	}
	r.Error = "Unsupported agent"
	return r
}

func (s *Service) Reset(ctx context.Context, attempt ResetAttempt) (ResetResult, error) {
	var candidates []Resetter
	for _, p := range s.providers {
		if reset, ok := p.(Resetter); ok && (attempt.Agent == "" || attempt.Agent == p.ID()) {
			candidates = append(candidates, reset)
		}
	}
	// Older desktop clients omitted Agent because only one integration could
	// reset quota. Keep that request valid only when its owner is unambiguous.
	if len(candidates) != 1 {
		return ResetResult{}, errors.New("Confirmed reset provider missing or unsupported")
	}
	return candidates[0].Reset(ctx, attempt)
}
