package main

import (
	"crypto/sha256"
	"fmt"
	"github.com/egoist/mygo"
	"log"
	"miya/internal/codex"
	"strconv"
	"time"
)

type taskNotice struct{ ID, Thread, Title, Body, ServerID, Agent, Project string }

func completionNotice(old, next Thread) (taskNotice, bool) {
	if !old.Busy || next.Busy || next.CompletedAtMs == 0 || next.Status == "Stopped" {
		return taskNotice{}, false
	}
	title := "Miya · 已完成"
	if next.Status != "" {
		title = "Miya · 任务结束"
	}
	return taskNotice{ID: fmt.Sprintf("miya-%d-%d", next.ID, next.CompletedAtMs), Thread: strconv.Itoa(next.ID), Title: title, Body: next.Title}, true
}
func (c *chatController) installNotifications() {
	set := c.setThreads
	c.setThreads = func(next []Thread) {
		previous := map[int]Thread{}
		for _, t := range c.threads() {
			previous[t.ID] = t
		}
		set(next)
		for _, t := range next {
			// An automation tells how its whole run ended instead.
			if old, ok := previous[t.ID]; ok && t.Automation == "" {
				if n, yes := completionNotice(old, t); yes {
					c.deliverNotice(n, "completion")
				}
			}
		}
	}
}
func (c *chatController) installApprovalNotifications() {
	set := c.setApprovals
	c.setApprovals = func(next []codex.Approval) {
		previous := map[string]bool{}
		for _, a := range c.approvals() {
			previous[string(a.ID)] = true
		}
		set(next)
		for _, a := range next {
			if previous[string(a.ID)] {
				continue
			}
			// Claude Code's approvals and background projects' too.
			if t, ok := c.approvalThread(a); ok {
				sum := sha256.Sum256(append([]byte(a.ThreadID), a.ID...))
				c.deliverNotice(taskNotice{ID: fmt.Sprintf("miya-wait-%x", sum[:16]), Thread: strconv.Itoa(t.ID), Title: "Miya · 需要你的确认", Body: t.Title}, "waiting")
			}
		}
	}
}
func (c *chatController) deliverNotice(n taskNotice, kind string) {
	if loadSetting("notifications-"+kind) == "off" {
		return
	}
	if c.push != nil {
		if id, err := strconv.Atoi(n.Thread); err == nil {
			if target := c.phoneThread(id); target.found {
				n.ServerID, n.Agent, n.Project = target.thread.ServerID, threadAgent(target.thread), target.cwd
			}
		}
		c.push.publish(n)
		return
	}
	w := currentWindow()
	if c.remote != nil && mygo.Power.IdleTime() >= 5*time.Minute {
		return
	}
	if w == nil {
		return
	}
	id, _ := strconv.Atoi(n.Thread)
	if c.active != nil && c.active() == id && w.IsFocused() && !w.IsMinimized() {
		return
	}
	notice := mygo.NewNotification(mygo.NotificationOptions{ID: n.ID, Title: n.Title, Body: n.Body, Group: "miya-tasks", Data: map[string]string{"thread": n.Thread}})
	notice.OnClick(func() {
		w.Restore()
		w.Show()
		w.Focus()
		w.Update(func() {
			for _, t := range c.threads() {
				if t.ID == id && c.setActive != nil {
					c.setActive(id)
					break
				}
			}
		})
	})
	if e := notice.Show(); e != nil {
		log.Printf("miya: notification: %v", e)
		notice.Close()
	}
}

func (c *chatController) startNotificationPresence(cl *serviceClient) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		report := func() {
			c.dispatch(func() {
				if c.remote != cl {
					return
				}
				w := currentWindow()
				if w != nil {
					cl.notify("notification-presence", mygo.Power.IdleTime() < 5*time.Minute)
				}
			})
		}
		report()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				report()
			}
		}
	}()
}
