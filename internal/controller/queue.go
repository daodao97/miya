package controller

import (
	"strings"

	"miya/internal/chat/model"
	"miya/internal/svcproto"
)

func (c *Controller) enqueue(id int, req model.SendRequest) int {
	req.ThreadID, req.Steer = id, false
	q := model.QueuedMessage{ID: c.NextID(), SendRequest: req}
	c.Update(id, func(t *model.Thread) { t.Queue = append(append([]model.QueuedMessage(nil), t.Queue...), q) })
	return id
}

func (c *Controller) RemoveQueued(threadID, queuedID int) {
	if c.Remote != nil {
		c.forward("dequeue", svcproto.Queued{Thread: threadID, ID: queuedID})
		return
	}
	c.Update(threadID, func(t *model.Thread) {
		out := make([]model.QueuedMessage, 0, len(t.Queue))
		for _, q := range t.Queue {
			if q.ID != queuedID {
				out = append(out, q)
			}
		}
		t.Queue = out
	})
}

// resumeQueue sends a queue that a stopped turn paused.
func (c *Controller) ResumeQueue(id int) {
	if c.Remote != nil {
		c.forward("drainQueue", id)
		return
	}
	c.Update(id, func(t *model.Thread) { t.StopRequested = false })
	c.drainQueue(id)
}

// drainQueue sends every queued message once the conversation is idle, as
// one message of the next turn: their texts in order, a blank line apart,
// with all their attachments and the settings of the latest. A turn the
// user stopped pauses the queue.
func (c *Controller) drainQueue(id int) {
	if c.Remote != nil {
		return
	}
	t := c.ThreadWithID(id)
	if c.Collaboration.Reservation(id) != "" {
		return
	}
	if t.ID == 0 || len(t.Queue) == 0 || t.Busy || t.Loading || t.ReadOnly || t.StopRequested {
		return
	}
	queue := append([]model.QueuedMessage(nil), t.Queue...)
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
	req.Parts = model.MessageParts(own)
	c.Update(id, func(t *model.Thread) { t.Queue = nil })
	if c.Submit(req) == 0 {
		c.Update(id, func(t *model.Thread) { t.Queue = append(queue, t.Queue...) })
	}
}

// steerQueued moves a queued message into the running turn.
func (c *Controller) SteerQueued(threadID, queuedID int) {
	if c.Remote != nil {
		c.forward("steerQueued", svcproto.Queued{Thread: threadID, ID: queuedID})
		return
	}
	t := c.ThreadWithID(threadID)
	if !t.Busy || t.Loading {
		return
	}
	for _, q := range t.Queue {
		if q.ID != queuedID {
			continue
		}
		req := q.SendRequest
		req.ThreadID, req.Steer = threadID, true
		if c.BackendOf(t).Steer(t, req) {
			c.RemoveQueued(threadID, queuedID)
		}
		return
	}
}
