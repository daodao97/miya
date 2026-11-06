package controller

import (
	sha256 "crypto/sha256"
	fmt "fmt"
	log "log"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	model "miya/internal/model"
	notification "miya/internal/notification"
	uiruntime "miya/internal/uiruntime"
	strconv "strconv"
	time "time"

	mygo "github.com/egoist/mygo"
)

func (c *Controller) InstallNotifications() {
	set := c.SetThreads
	c.SetThreads = func(next []model.Thread) {
		previous := map[int]model.Thread{}
		for _, t := range c.Threads() {
			previous[t.ID] = t
		}
		set(next)
		for _, t := range next {
			if old, ok := previous[t.ID]; ok {
				c.RecordDesktopCompletion(old, t)
			}
			// An automation tells how its whole run ended instead.
			if old, ok := previous[t.ID]; ok && t.Automation == "" {
				if n, yes := notification.Completion(old, t); yes {
					c.deliverNotice(n, "completion")
				}
			}
		}
	}
}

func (c *Controller) installApprovalNotifications() {
	set := c.SetApprovals
	c.SetApprovals = func(next []codex.Approval) {
		previous := map[string]bool{}
		for _, a := range c.Approvals() {
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
				n := notification.Approval(t, a)
				n.ID = fmt.Sprintf("miya-wait-%x", sum[:16])
				c.deliverNotice(n, "waiting")
			}
		}
	}
}

func (c *Controller) deliverNotice(n notification.Notice, kind string) {
	if appmodel.LoadSetting("notifications-"+kind) == "off" {
		return
	}
	if c.Push != nil {
		if id, err := strconv.Atoi(n.Thread); err == nil {
			if target := c.phoneThread(id); target.Found {
				n.ServerID, n.Agent, n.Project = target.Thread.ServerID, model.ThreadAgent(target.Thread), target.cwd
			}
		}
		c.Push.Publish(n)
		return
	}
	w := uiruntime.CurrentWindow()
	if c.Remote != nil && mygo.Power.IdleTime() >= 5*time.Minute {
		return
	}
	if w == nil {
		return
	}
	id, _ := strconv.Atoi(n.Thread)
	if (c.WorkspaceVisible == nil || c.WorkspaceVisible()) && c.Active != nil && c.Active() == id && w.IsFocused() && !w.IsMinimized() {
		return
	}
	notice := mygo.NewNotification(mygo.NotificationOptions{ID: n.ID, Title: tr(n.Title), Body: n.Body, Group: "miya-tasks", Data: map[string]string{"thread": n.Thread}})
	notice.OnClick(func() {
		w.Restore()
		w.Show()
		w.Focus()
		w.Update(func() {
			if c.RevealWorkspace != nil {
				c.RevealWorkspace()
			}
			for _, t := range c.Threads() {
				if t.ID == id && c.SetActive != nil {
					c.SetActive(id)
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

func (c *Controller) StartNotificationPresence(cl *appmodel.ServiceClient) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		report := func() {
			c.Dispatch(func() {
				if c.Remote != cl {
					return
				}
				w := uiruntime.CurrentWindow()
				if w != nil && (c.WorkspaceVisible == nil || c.WorkspaceVisible()) {
					cl.Notify("notification-presence", mygo.Power.IdleTime() < 5*time.Minute)
				}
			})
		}
		select {
		case <-c.Ctx.Done():
			return
		case <-cl.Done():
			return
		default:
		}
		report()
		for {
			select {
			case <-c.Ctx.Done():
				return
			case <-cl.Done():
				return
			case <-ticker.C:
				report()
			}
		}
	}()
	return done
}
