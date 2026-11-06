package main

import (
	appmodel "miya/internal/appmodel"
	conversation "miya/internal/conversation"
	os "os"
	testing "testing"
)

func TestClaudeToolTitles(t *testing.T) {
	home, _ := os.UserHomeDir()
	tool := func(content, status string) appmodel.Message {
		return appmodel.Message{Role: "tool", Kind: "claudeTool", Content: content, Status: status}
	}
	for _, tc := range []struct {
		m          appmodel.Message
		title, ico string
	}{
		{tool("Bash\n{\"command\":\"rtk ls -la\\necho hi\",\"description\":\"List\"}\nout", "completed"), "Ran rtk ls -la", "terminal-2"},
		{tool("Bash\n{\"command\":\"go test\"}", "inProgress"), "Running go test", "terminal-2"},
		{tool("Read\n{\"file_path\":\""+home+"/w/main.go\"}\npackage main", "completed"), "Read ~/w/main.go", "file-text"},
		{tool("Edit\n{\"file_path\":\"/tmp/a.go\",\"old_string\":\"x\"}", "failed"), "Failed · /tmp/a.go", "edit"},
		{tool("Grep\n{\"pattern\":\"func main\"}", "completed"), "Searched func main", "folder"},
		{tool("mcp__x__lookup\n{\"query\":\"weather\"}", "completed"), "mcp__x__lookup · weather", ""},
		{tool("Read", "completed"), "Read", "file-text"},
	} {
		if got := conversation.ActivityTitle(tc.m); got != tc.title {
			t.Errorf("title %q, want %q", got, tc.title)
		}
		if tc.ico != "" && appmodel.ActivityIcon(tc.m) != tc.ico {
			t.Errorf("%q icon %q, want %q", tc.title, appmodel.ActivityIcon(tc.m), tc.ico)
		}
	}
}
