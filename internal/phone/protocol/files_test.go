package protocol

import "testing"

// File URLs must remain usable across desktop and phone versions.
func TestProjectFileIDCompatibility(t *testing.T) {
	for _, tc := range []struct{ root, id string }{
		{"", "e3b0c44298fc1c149afbf4c8996fb924"},
		{"/workspace/Miya", "b6b639bbe3c44a0c55d5c7785cbf1940"},
		{"/workspace/项目 with spaces", "f5cc8132db59969c161608b0a2cb666a"},
	} {
		if got := ProjectFileID(tc.root); got != tc.id {
			t.Errorf("project %q: got %s, want %s", tc.root, got, tc.id)
		}
	}
}
