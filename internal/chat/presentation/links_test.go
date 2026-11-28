package presentation

import "testing"

func TestRemoteFileLink(t *testing.T) {
	for _, tc := range []struct {
		cwd, link, want string
	}{
		{"/workspace/miya", "/workspace/miya-worktree/build/screenshot.png", "/workspace/miya-worktree/build/screenshot.png"},
		{"/workspace/miya", "../miya-worktree/notes.md:12:4", "/workspace/miya-worktree/notes.md"},
		{"", "file:///private/tmp/%E5%9B%BE%20%23.png", "/private/tmp/图 #.png"},
		{"", "notes.md", ""},
		{"", "C:/build/screenshot.png", "C:/build/screenshot.png"},
		{"C:\\workspace\\miya", "D:\\build\\screenshot.png", "D:/build/screenshot.png"},
		{"C:\\workspace\\miya", "file:///D:/build/screenshot.png", "D:/build/screenshot.png"},
		{"C:\\workspace\\miya", "../screenshot.png", "C:/workspace/screenshot.png"},
		{"/workspace/miya", "https://example.com/screenshot.png", ""},
		{"/workspace/miya", "//example.com/screenshot.png", ""},
		{"/workspace/miya", "file://example.com/screenshot.png", ""},
	} {
		got, ok := RemoteFileLink(tc.cwd, tc.link)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("RemoteFileLink(%q, %q) = %q, %v, want %q", tc.cwd, tc.link, got, ok, tc.want)
		}
	}
}
