//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/agent/claude/native/transcript"
	"miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/memory"
	"miya/internal/model"
	"miya/internal/phone"
	"miya/internal/phoneapi"
	"miya/internal/remote"
	"miya/internal/thumbnail"
)

func TestPhonePreviewRequestChecksConversationAndWorksBeforeSubscribe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	source := transcript.LocalImageURI(path)
	c := serverController(t.TempDir(), "")
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "user", Images: []string{source}}}}})
	conn, _ := serveTestPhone(t, c)
	for _, ref := range []string{source, "file:///unmentioned/private.png"} {
		conn.requests <- remote.Request{Op: "image", Thread: 1, ServerID: "s1", ImageSource: ref}
		for {
			select {
			case frame := <-conn.frames:
				if frame.Type != "image" {
					continue
				}
				var preview thumbnail.Image
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
	c.ClaudeModel, c.ClaudeEffort = "claude-opus-5-5", "high"
	r := remote.Request{Thread: 1, ServerID: "s1", Agent: appmodel.AgentClaude, Text: "later", Options: &remote.SendOptions{Model: "claude-sonnet-5-5", Effort: "low", Policy: "on-request"}}
	result := c.PhoneSend(r, nil)
	if result.Err != "" || result.Thread != 1 {
		t.Fatalf("busy send was refused: %+v", result)
	}
	th := c.ThreadWithID(1)
	if len(th.Queue) != 1 {
		t.Fatal("phone message was not queued")
	}
	q := th.Queue[0]
	if q.Model != r.Options.Model || q.Effort != "low" || !q.ExplicitSettings || c.ClaudeModel != "claude-opus-5-5" {
		t.Fatalf("settings were lost or desktop defaults changed: %+v", q)
	}
	if err := c.PhoneQueueAction(remote.Request{Op: "dequeue", Thread: 1, ServerID: "s1", QueuedID: q.ID}); err != nil || len(c.ThreadWithID(1).Queue) != 0 {
		t.Fatal("queue item was not removed", err)
	}
	r.Steer = true
	if result := c.PhoneSend(r, nil); result.Err != "" || len(c.ThreadWithID(1).Queue) != 1 {
		t.Fatal("unavailable steer should fall back to queue")
	}
	r.Options.Model = "unknown"
	if result := c.PhoneSend(r, nil); result.Err == "" || len(c.ThreadWithID(1).Queue) != 1 {
		t.Fatal("invalid model changed the queue")
	}
}

func TestPhoneModelChoicesValidatePerModelEffort(t *testing.T) {
	c := serverController(t.TempDir(), "")
	var model codex.Model
	if err := json.Unmarshal([]byte(`{"model":"test-model","isDefault":true,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"medium"},{"reasoningEffort":"high"}]}`), &model); err != nil {
		t.Fatal(err)
	}
	c.SetModels([]codex.Model{model})
	c.SetModel("test-model")
	c.SetEffort("high")
	r := remote.Request{Options: &remote.SendOptions{Model: "test-model", Effort: "low", Policy: "on-request"}}
	if _, err := c.PhoneSendRequest(r, nil, appmodel.AgentCodex); err == nil {
		t.Fatal("accepted an unsupported effort")
	}
	r.Options.Effort = "medium"
	if req, err := c.PhoneSendRequest(r, nil, appmodel.AgentCodex); err != nil || req.Model != "test-model" || req.Effort != "medium" {
		t.Fatalf("valid choice refused: %+v %v", req, err)
	}
	r.Options.Policy = "not-a-policy"
	if _, err := c.PhoneSendRequest(r, nil, appmodel.AgentCodex); err == nil {
		t.Fatal("invalid policy accepted")
	}
}

func TestPhoneApprovalAcknowledgementReportsValidationFailure(t *testing.T) {
	c := serverController(t.TempDir(), "")
	a := codex.Approval{ID: json.RawMessage(`1`), Method: "item/tool/requestUserInput", Questions: []codex.Question{{ID: "q"}}}
	c.SetApprovals([]codex.Approval{a})
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "respond", Approval: a.ID, Result: json.RawMessage(`{"answers":{"wrong":{"answers":["x"]}}}`)}
	for {
		select {
		case f := <-conn.frames:
			if f.Type == "responded" {
				if f.Error == "" {
					t.Fatal("missing validation error")
				}
				var reply phoneapi.ApprovalReply
				if json.Unmarshal(f.Data, &reply) != nil || !phone.SameJSON(reply.ID, a.ID) {
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

type binaryPreviewConnection struct {
	*testPhoneConnection
	source string
	image  []byte
}

func (c *binaryPreviewConnection) SendImage(source string, data []byte) error {
	c.source, c.image = source, data
	return nil
}

func TestPhonePreviewBinaryPathKeepsAuthorization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	source := transcript.LocalImageURI(path)
	c := serverController(t.TempDir(), "")
	c.Run = func(f func()) { f() }
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: []model.Message{{ID: 1, Images: []string{source}}}}})
	conn := &binaryPreviewConnection{}
	var frames []remote.Frame
	send := func(f remote.Frame) bool { frames = append(frames, f); return true }
	if !c.SendPhonePreview(context.Background(), conn, remote.Request{Thread: 1, ServerID: "s1", ImageSource: source}, false, send) {
		t.Fatal("preview failed")
	}
	if conn.source != source || len(conn.image) == 0 || len(frames) != 0 {
		t.Fatal("binary preview was encoded into a control payload")
	}
	conn.source, conn.image = "", nil
	if !c.SendPhonePreview(context.Background(), conn, remote.Request{Thread: 1, ServerID: "s1", ImageSource: "file:///private/unmentioned.png"}, false, send) {
		t.Fatal("error reply failed")
	}
	if conn.source != "" || len(conn.image) != 0 || len(frames) != 1 || frames[0].Error == "" {
		t.Fatal("binary path bypassed source authorization")
	}
}

func TestPhoneMemoryIsExplicitReadOnlyAndAdvertised(t *testing.T) {
	c := serverController(t.TempDir(), "")
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.Cwd)
	now := time.Now()
	e, err := c.Memories().Store.Save(scope, memory.Mutation{Kind: "fact", Content: "暂缓手机协议升级", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	if !c.PhoneSnapshot().Memory {
		t.Fatal("memory capability missing")
	}
	// Normal state carries capability/revision, never the library.
	b, _ := json.Marshal(c.PhoneSnapshot())
	if strings.Contains(string(b), e.Content) {
		t.Fatal("library leaked into state")
	}
	conn, _ := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "memory", Project: c.Cwd, MemoryScope: "project"}
	for {
		select {
		case f := <-conn.frames:
			if f.Type != "memory" {
				continue
			}
			var result phoneapi.MemoryResult
			if f.Error != "" || json.Unmarshal(f.Data, &result) != nil || len(result.List.Entries) != 1 || result.List.Entries[0].ID != e.ID {
				t.Fatalf("memory response: %s %s", f.Error, f.Data)
			}
			return
		case <-conn.ctx.Done():
			t.Fatal("no memory response")
		}
	}
}
