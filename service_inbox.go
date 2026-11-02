package main

import "sync"

// serviceInbox gathers the events a service pushes between two turns of the
// main thread, so that a burst of them costs one dispatch and one frame
// rather than one each. States are whole snapshots: of a batch only the
// latest is applied, in its place; messages and drafts are applied in order.
type serviceInbox struct {
	mu        sync.Mutex
	queued    []svcMessage
	scheduled bool
}

// push queues m and reports whether the caller must schedule a drain: only
// the first event since the last take does.
func (b *serviceInbox) push(m svcMessage) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queued = append(b.queued, m)
	if b.scheduled {
		return false
	}
	b.scheduled = true
	return true
}

// take returns the queued events in order, without the states a later state
// of the same batch replaces.
func (b *serviceInbox) take() []svcMessage {
	b.mu.Lock()
	queued := b.queued
	b.queued, b.scheduled = nil, false
	b.mu.Unlock()
	last := -1
	for i, m := range queued {
		if m.State != nil {
			last = i
		}
	}
	out := queued[:0]
	for i, m := range queued {
		if m.State != nil && i != last {
			continue
		}
		out = append(out, m)
	}
	return out
}
