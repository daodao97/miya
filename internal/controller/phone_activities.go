package controller

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/widget"
	"miya/internal/apns"
	"miya/internal/appdir"
	"miya/internal/async"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

// activityProvider opens only desktop-owned credentials. Phone requests contain
// tokens and durable references, never provider keys, topics or payloads.
type activityProvider struct{}

func (activityProvider) Send(ctx context.Context, n apns.Notification) (apns.Response, error) {
	p, err := apns.OpenProvider(appdir.Dir())
	if err != nil {
		return apns.Response{}, err
	}
	if p.Topic() != "com.daodao.miya" {
		return apns.Response{}, errors.New("APNs topic must be com.daodao.miya")
	}
	return p.Send(ctx, n)
}

func (c *Controller) startPhoneActivities() {
	c.Activities, _ = apns.NewActivityManager(activityProvider{}, apns.ActivityManagerOptions{})
	manager := c.Activities
	ctx := c.Ctx
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			// Snapshot UI-owned threads on the dispatcher, send off it. Subscriptions
			// outlive a phone connection so APNs can finish activities in background.
			updates, ok := async.OnMain(ctx, c.Dispatch, func() []activityDelivery { return c.phoneActivityUpdates(manager) })
			if !ok {
				return
			}
			for _, u := range updates {
				if err := manager.Publish(ctx, u.target, u.update); err != nil && !errors.Is(err, apns.ErrActivityChanged) {
					log.Printf("miya: live activity delivery: %v", err)
				}
			}
		}
	}()
}

type activityDelivery struct {
	target apns.ActivityTarget
	update apns.ActivityUpdate
}

func (c *Controller) phoneActivityUpdates(manager *apns.ActivityManager) []activityDelivery {
	var out []activityDelivery
	for _, target := range manager.Targets() {
		reference := target.ResourceID
		if raw, _, ok := phoneproto.ParseActivityURL(reference); ok {
			reference = raw
		}
		ref, ok := model.ParseThreadURL(reference)
		if !ok || ref.Source != c.ReferenceSource {
			manager.Remove(target.ID)
			continue
		}
		t := c.PhoneThreadFor(remote.Request{ServerID: ref.SessionID, Agent: ref.Backend})
		body := tr("Running")
		end := !t.Found || !t.Thread.Busy
		if end {
			body = tr("Done")
		}
		title := t.Thread.Title
		if title == "" {
			title = "Miya"
		}
		content, err := widget.ActivityState(widget.ActivityContent{Title: title, Body: body, Symbol: "bubble.left.and.bubble.right", URL: target.ResourceID, Stale: time.Now().Add(2 * time.Minute)})
		if err != nil {
			continue
		}
		out = append(out, activityDelivery{target, apns.ActivityUpdate{ContentState: content, DeduplicationKey: title + "\n" + body, End: end}})
	}
	return out
}

func (ps *phoneSession) onActivity(r remote.Request) bool {
	var registration phoneproto.ActivityRegistration
	var message string
	if json.Unmarshal(r.Activity, &registration) != nil {
		message = tr("Invalid notification registration")
	} else {
		result, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() string {
			if err := ps.c.registerPhoneActivity(registration); err != nil {
				return err.Error()
			}
			return ""
		})
		if !ok {
			return false
		}
		message = result
	}
	return ps.send(remote.Frame{Type: "activity", Error: message})
}

func (c *Controller) registerPhoneActivity(r phoneproto.ActivityRegistration) error {
	if c.Activities == nil {
		return errors.New(tr("Notification service unavailable"))
	}
	if len(r.DeviceID) != 32 || len(r.ID) == 0 || len(r.ID) > 128 || strings.ContainsAny(r.ID, "\r\n\x00") {
		return errors.New(tr("Invalid notification registration"))
	}
	if _, err := hex.DecodeString(r.DeviceID); err != nil {
		return errors.New(tr("Invalid notification registration"))
	}
	id := strings.ToLower(r.DeviceID) + ":" + r.ID
	if r.Token == "" {
		c.Activities.Remove(id)
		return nil
	}
	ref, ok := model.ParseThreadURL(r.Reference)
	if !ok || ref.Source != c.ReferenceSource || !c.PhoneThreadFor(remote.Request{ServerID: ref.SessionID, Agent: ref.Backend}).Found {
		return errors.New(tr("Conversation is no longer on the desktop"))
	}
	if r.Desktop != "" {
		if len(r.Desktop) > 64 {
			return errors.New(tr("Invalid notification registration"))
		}
		if _, err := hex.DecodeString(r.Desktop); err != nil {
			return errors.New(tr("Invalid notification registration"))
		}
	}
	return c.Activities.Register(apns.ActivityRegistration{ID: id, DeviceToken: r.Token, ResourceID: phoneproto.ActivityURL(ref.URL(), r.Desktop)})
}
