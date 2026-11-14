package presentation

import (
	"reflect"
	"testing"

	"miya/internal/chat/model"
)

// An attached image shows once, as an image: not again as a file chip, while
// a resend still carries it.
func TestMessageFilesLeaveOutImages(t *testing.T) {
	shot := "/Users/me/Library/Application Support/CleanShot/shot.png"
	m := model.Message{
		Images:      []string{"file:///Users/me/Library/Application%20Support/CleanShot/shot.png"},
		Attachments: []string{shot, "/tmp/notes.md", "/tmp/notes.md"},
	}
	if got, want := MessageFiles(m), []string{"/tmp/notes.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("MessageFiles = %q, want %q", got, want)
	}
	if got := MessageAttachments(m); len(got) == 0 || got[0] != shot {
		t.Fatalf("MessageAttachments = %q, want it to keep %q for a resend", got, shot)
	}

	// An image known only by its URL, from an older transcript, still is no file.
	m = model.Message{Images: []string{"file:///tmp/old.png"}}
	if got := MessageFiles(m); len(got) != 0 {
		t.Fatalf("MessageFiles = %q, want none", got)
	}
	if got, want := MessageAttachments(m), []string{"/tmp/old.png"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("MessageAttachments = %q, want %q", got, want)
	}
}
