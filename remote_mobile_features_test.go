//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"miya/internal/codex"
	"miya/internal/remote"
)

func TestPhonePreviewRequestChecksConversationAndWorksBeforeSubscribe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	source := localImageDataURI(path)
	c := serverController(t.TempDir(), "")
	c.setThreads([]Thread{{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: []Message{{ID: 1, Role: "user", Images: []string{source}}}}})
	conn, _ := serveTestPhone(t, c)
	for _, ref := range []string{source, "file:///unmentioned/private.png"} {
		conn.requests <- remote.Request{Op: "image", Thread: 1, ServerID: "s1", ImageSource: ref}
		for {
			select {
			case frame := <-conn.frames:
				if frame.Type != "image" {
					continue
				}
				var preview phoneImagePreview
				if err := json.Unmarshal(frame.Data, &preview); err != nil || preview.Source != ref {
					t.Fatalf("wrong preview reply: %v", err)
				}
				if ref == source {
					if frame.Error != "" || len(preview.Data) == 0 {
						t.Fatal("cached message could not fetch its image before subscribe", frame.Error)
					}
				} else if frame.Error == "" || len(preview.Data) != 0 {
					t.Fatal("unmentioned file was exposed")
				}
			case <-conn.ctx.Done():
				t.Fatal("no preview response")
			}
			break
		}
	}
}

func TestPhoneQueueRetainsPerMessageModelAndCanRemove(t *testing.T) {
	c, _ := queueController(t)
	c.claudeModel, c.claudeEffort = "claude-opus-5-5", "high"
	r := remote.Request{Thread: 1, ServerID: "s1", Agent: agentClaude, Text: "later", Options: &remote.SendOptions{Model: "claude-sonnet-5-5", Effort: "low", Policy: "on-request"}}
	result := c.phoneSend(r, nil)
	if result.err != "" || result.thread != 1 {
		t.Fatalf("busy send was refused: %+v", result)
	}
	th := c.threadWithID(1)
	if len(th.Queue) != 1 {
		t.Fatal("phone message was not queued")
	}
	q := th.Queue[0]
	if q.Model != r.Options.Model || q.Effort != "low" || !q.ExplicitSettings || c.claudeModel != "claude-opus-5-5" {
		t.Fatalf("settings were lost or desktop defaults changed: %+v", q)
	}
	if err := c.phoneQueueAction(remote.Request{Op: "dequeue", Thread: 1, ServerID: "s1", QueuedID: q.ID}); err != nil || len(c.threadWithID(1).Queue) != 0 {
		t.Fatal("queue item was not removed", err)
	}
	r.Steer = true
	if result := c.phoneSend(r, nil); result.err != "" || len(c.threadWithID(1).Queue) != 1 {
		t.Fatal("unavailable steer should fall back to queue")
	}
	r.Options.Model = "unknown"
	if result := c.phoneSend(r, nil); result.err == "" || len(c.threadWithID(1).Queue) != 1 {
		t.Fatal("invalid model changed the queue")
	}
}

func TestPhoneModelChoicesValidatePerModelEffort(t *testing.T) {
	c := serverController(t.TempDir(), "")
	var model codex.Model
	if err := json.Unmarshal([]byte(`{"model":"test-model","isDefault":true,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"medium"},{"reasoningEffort":"high"}]}`), &model); err != nil {
		t.Fatal(err)
	}
	c.setModels([]codex.Model{model})
	c.setModel("test-model")
	c.setEffort("high")
	r := remote.Request{Options: &remote.SendOptions{Model: "test-model", Effort: "low", Policy: "on-request"}}
	if _, err := c.phoneSendRequest(r, nil, agentCodex); err == nil {
		t.Fatal("accepted an unsupported effort")
	}
	r.Options.Effort = "medium"
	if req, err := c.phoneSendRequest(r, nil, agentCodex); err != nil || req.Model != "test-model" || req.Effort != "medium" {
		t.Fatalf("valid choice refused: %+v %v", req, err)
	}
	r.Options.Policy = "not-a-policy"
	if _, err := c.phoneSendRequest(r, nil, agentCodex); err == nil {
		t.Fatal("invalid policy accepted")
	}
}

func TestPhoneApprovalAcknowledgementReportsValidationFailure(t *testing.T) {
	c := serverController(t.TempDir(), "")
	a := codex.Approval{ID: json.RawMessage(`1`), Method: "item/tool/requestUserInput", Questions: []codex.Question{{ID: "q"}}}
	c.setApprovals([]codex.Approval{a})
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "respond", Approval: a.ID, Result: json.RawMessage(`{"answers":{"wrong":{"answers":["x"]}}}`)}
	for {
		select {
		case f := <-conn.frames:
			if f.Type == "responded" {
				if f.Error == "" {
					t.Fatal("missing validation error")
				}
				var reply phoneApprovalReply
				if json.Unmarshal(f.Data, &reply) != nil || !sameJSON(reply.ID, a.ID) {
					t.Fatal("ack identified the wrong request")
				}
				return
			}
		case <-conn.ctx.Done():
			t.Fatal("no approval acknowledgement")
			return
		}
	}
}
