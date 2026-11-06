package controller

import (
	appmodel "miya/internal/appmodel"
	model "miya/internal/model"
	strings "strings"
)

func (c *Controller) enqueue(id int, req appmodel.SendRequest) int {
	req.ThreadID, req.Steer = id, false
	q := appmodel.QueuedMessage{ID: c.NextID(), SendRequest: req}
	c.Update(id, func(t *appmodel.Thread) { t.Queue = append(append([]appmodel.QueuedMessage(nil), t.Queue...), q) })
	return id
}

func (c *Controller) RemoveQueued(threadID, queuedID int) {
	if c.Remote != nil {
		c.forward("dequeue", appmodel.SvcQueued{Thread: threadID, ID: queuedID})
		return
	}
	c.Update(threadID, func(t *appmodel.Thread) {
		out := make([]appmodel.QueuedMessage, 0, len(t.Queue))
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
	c.Update(id, func(t *appmodel.Thread) { t.StopRequested = false })
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
	queue := append([]appmodel.QueuedMessage(nil), t.Queue...)
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
	req.Parts = appmodel.MessageParts(own)
	c.Update(id, func(t *appmodel.Thread) { t.Queue = nil })
	if c.Submit(req) == 0 {
		c.Update(id, func(t *appmodel.Thread) { t.Queue = append(queue, t.Queue...) })
	}
}

// steerQueued moves a queued message into the running turn.
func (c *Controller) SteerQueued(threadID, queuedID int) {
	if c.Remote != nil {
		c.forward("steerQueued", appmodel.SvcQueued{Thread: threadID, ID: queuedID})
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
		if BackendOf(t).Steer(c, t, req) {
			c.RemoveQueued(threadID, queuedID)
		}
		return
	}
}
