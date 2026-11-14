//go:build !mygo_ui

package integration

import (
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"
)

func failedResponse() model.Thread {
	return model.Thread{ID: 1, ServerID: "s1", Resumed: true, Loaded: true, HistoryLoaded: true, Messages: []model.Message{
		{ID: 10, Role: "user", Content: "安装应用"},
		{ID: 11, Role: "tool", Content: "构建完成", Status: "completed"},
		{ID: 12, Role: "error", Content: "API Error: Connection dropped (ECONNRESET)."},
	}}
}

func TestRetryMessageKeepsAttachmentsAndReferenceSnapshot(t *testing.T) {
	src := handoffSource()
	raw := model.ReferenceText(testReference(src), src.Title) + " 再审核一下"
	prompt, _, err := controller.ExpandThreadRefs(raw, model.ThreadRef{}, func(model.ThreadRef) (model.Thread, []model.Message, error) {
		return src, src.Messages, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(t.TempDir(), "screen shot.png"), filepath.Join(t.TempDir(), "notes.txt")}
	m := model.Message{Content: prompt + "\n📎 notes.txt", Attachments: paths, Images: []string{(&url.URL{Scheme: "file", Path: paths[0]}).String()}}
	if got := msgfmt.MessagePrompt(m); got != prompt {
		t.Fatal("retry changed the original reference or its context snapshot")
	}
	if got := msgfmt.MessageAttachments(m); !reflect.DeepEqual(got, paths) {
		t.Fatalf("retry lost or duplicated attachment paths: %q", got)
	}
	// Older messages only recorded the image URL, including escaped spaces.
	m = model.Message{Images: m.Images}
	if got := msgfmt.MessageAttachments(m); !reflect.DeepEqual(got, paths[:1]) || msgfmt.MessagePrompt(m) != "" {
		t.Fatalf("retry lost an older image-only prompt: %q", got)
	}
}

func TestFailedAfterOutput(t *testing.T) {
	apiError := model.Message{Role: "assistant", Content: "API Error: 503 no healthy node with capacity supports this model."}
	for _, tc := range []struct {
		name     string
		messages []model.Message
		want     bool
	}{
		{name: "error only"},
		{name: "API error text", messages: []model.Message{apiError}},
		{name: "empty assistant", messages: []model.Message{{Role: "assistant"}}},
		{name: "whitespace assistant", messages: []model.Message{{Role: "assistant", Content: " \n\t"}}},
		{name: "marked API error", messages: []model.Message{{Role: "assistant", Status: "failed", Content: "服务暂不可用"}}},
		{name: "reasoning only", messages: []model.Message{{Role: "activity", Kind: "reasoning", Content: "thinking"}}},
		{name: "real reply", messages: []model.Message{{Role: "assistant", Content: "已完成检查"}}, want: true},
		{name: "reply then API error", messages: []model.Message{{Role: "assistant", Content: "部分结果"}, apiError}, want: true},
		{name: "tool then API error", messages: []model.Message{{Role: "tool", Content: "构建完成", Status: "completed"}, apiError}, want: true},
		{name: "tool without result", messages: []model.Message{{Role: "tool", Status: "failed"}, apiError}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Previous turns must never make an empty failed turn look useful.
			messages := []model.Message{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "earlier reply"}, {Role: "user", Content: "同步上游"}}
			messages = append(messages, tc.messages...)
			messages = append(messages, model.Message{Role: "error", Content: apiError.Content})
			if got := msgfmt.FailedAfterOutput(model.Thread{Messages: messages}, 2); got != tc.want {
				t.Fatalf("FailedAfterOutput = %v, want %v", got, tc.want)
			}
		})
	}
}
