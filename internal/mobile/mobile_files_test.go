package mobile

import (
	"testing"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
)

func TestMessageFileTargetUsesConversationAndRegisteredProject(t *testing.T) {
	m := NewMobileApp()
	m.State = phoneproto.State{Projects: []model.ProjectSnapshot{
		{Path: "/workspace/Miya", Threads: []model.Thread{{ID: 7}}},
		{Path: "/workspace/mygo"},
		{Path: "/workspace/mygo/nested"},
		{Path: "/"},
	}}
	m.Open, m.Selected = 7, "/workspace/mygo"
	for _, tc := range []struct{ path, project, file string }{
		{"docs/report.md", "/workspace/Miya", "docs/report.md"},
		{"../mygo/internal/android/backend/work_queue.go", "/workspace/mygo", "internal/android/backend/work_queue.go"},
		{"../mygo/internal/android/backend/native_requests.go", "/workspace/mygo", "internal/android/backend/native_requests.go"},
		{"../mygo/android/app/src/main/java/org/mygo/MygoSelection.kt", "/workspace/mygo", "android/app/src/main/java/org/mygo/MygoSelection.kt"},
		{"../mygo/android/jni/local_time.go", "/workspace/mygo", "android/jni/local_time.go"},
		{"../mygo/nested/空 格 #?.md", "/workspace/mygo/nested", "空 格 #?.md"},
	} {
		project, file, ok := m.messageFileTarget(tc.path)
		if !ok || project != tc.project || file != tc.file {
			t.Fatalf("%q: got %q %q %v", tc.path, project, file, ok)
		}
	}
	for _, name := range []string{"../../private/secret.txt", "../mygo-other/secret.txt"} {
		if _, _, ok := m.messageFileTarget(name); ok {
			t.Fatalf("unregistered file accepted: %q", name)
		}
	}
}
