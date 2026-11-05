package main

import (
	"miya/internal/model"
	"strings"
)

// A conversation that is running queues what is sent to it, for the agent
// to receive once the turn ends, as the Codex and Claude Code CLIs do. The
// queue lives in the conversation, in the process running the agents, so it
// is sent even after the window quits.

func (c *chatController) enqueue(id int, req SendRequest) int {
	req.ThreadID, req.Steer = id, false
	q := QueuedMessage{ID: c.nextID(), SendRequest: req}
	c.update(id, func(t *Thread) { t.Queue = append(append([]QueuedMessage(nil), t.Queue...), q) })
	return id
}

func (c *chatController) removeQueued(threadID, queuedID int) {
	if c.remote != nil {
		c.forward("dequeue", svcQueued{Thread: threadID, ID: queuedID})
		return
	}
	c.update(threadID, func(t *Thread) {
		out := make([]QueuedMessage, 0, len(t.Queue))
		for _, q := range t.Queue {
			if q.ID != queuedID {
				out = append(out, q)
			}
		}
		t.Queue = out
	})
}

// resumeQueue sends a queue that a stopped turn paused.
func (c *chatController) resumeQueue(id int) {
	if c.remote != nil {
		c.forward("drainQueue", id)
		return
	}
	c.update(id, func(t *Thread) { t.StopRequested = false })
	c.drainQueue(id)
}

// drainQueue sends every queued message once the conversation is idle, as
// one message of the next turn: their texts in order, a blank line apart,
// with all their attachments and the settings of the latest. A turn the
// user stopped pauses the queue.
func (c *chatController) drainQueue(id int) {
	if c.remote != nil {
		return
	}
	t := c.threadWithID(id)
	if c.managedReservations[id] != "" {
		return
	}
	if t.ID == 0 || len(t.Queue) == 0 || t.Busy || t.Loading || t.ReadOnly || t.StopRequested {
		return
	}
	queue := append([]QueuedMessage(nil), t.Queue...)
	req := queue[len(queue)-1].SendRequest
	req.ThreadID, req.Steer, req.Attachments = id, false, nil
	req.MemoryControl = false
	texts := make([]string, 0, len(queue))
	for _, q := range queue {
		req.MemoryControl = req.MemoryControl || q.MemoryControl
		if s := strings.TrimSpace(q.Text); s != "" {
			texts = append(texts, s)
		}
		req.Attachments = append(req.Attachments, q.Attachments...)
	}
	req.Text = model.JoinReferenceMessages(texts)
	own, _ := model.SplitReferenceContext(req.Text)
	req.Parts = MessageParts(own)
	c.update(id, func(t *Thread) { t.Queue = nil })
	if c.submit(req) == 0 {
		c.update(id, func(t *Thread) { t.Queue = append(queue, t.Queue...) })
	}
}

// steerQueued moves a queued message into the running turn.
func (c *chatController) steerQueued(threadID, queuedID int) {
	if c.remote != nil {
		c.forward("steerQueued", svcQueued{Thread: threadID, ID: queuedID})
		return
	}
	t := c.threadWithID(threadID)
	if !t.Busy || t.Loading {
		return
	}
	for _, q := range t.Queue {
		if q.ID != queuedID {
			continue
		}
		req := q.SendRequest
		req.ThreadID, req.Steer = threadID, true
		if backendOf(t).Steer(c, t, req) {
			c.removeQueued(threadID, queuedID)
		}
		return
	}
}
