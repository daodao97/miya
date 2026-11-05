// Package async holds the reply a service request gives when its work must
// run off the main thread.
package async

import (
	"context"
	"sync"
)

// Reply is a reply worked out off the main thread, for requests that wait on
// other programs; the service runs it before it replies.
type Reply func() (any, error)

// Executor runs functions one at a time, in order, on the goroutine that
// loops it: the service's main thread.
type Executor struct {
	mu   sync.Mutex
	q    []func()
	wake chan struct{}
}

func NewExecutor() *Executor { return &Executor{wake: make(chan struct{}, 1)} }

func (e *Executor) Push(f func()) {
	e.mu.Lock()
	e.q = append(e.q, f)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Executor) Loop(quit <-chan struct{}) {
	for {
		e.mu.Lock()
		q := e.q
		e.q = nil
		e.mu.Unlock()
		for _, f := range q {
			f()
		}
		if len(q) > 0 {
			continue
		}
		select {
		case <-e.wake:
		case <-quit:
			return
		}
	}
}

// do runs f on the executor and waits for it.
func (e *Executor) Do(f func()) {
	done := make(chan struct{})
	e.Push(func() { f(); close(done) })
	<-done
}

// OnMain runs f on the UI thread and waits for its result.
func OnMain[T any](ctx context.Context, dispatch func(func()), f func() T) (T, bool) {
	ch := make(chan T, 1)
	dispatch(func() { ch <- f() })
	select {
	case v := <-ch:
		return v, true
	case <-ctx.Done():
		var zero T
		return zero, false
	}
}
