package runtime

import "testing"

func TestSessionEnvironmentReadsOnlyExactMarker(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"SECRET=hidden\x00CODEX_THREAD_ID=session-a\x00OTHER=value\x00", "session-a"},
		{"ARG=CODEX_THREAD_ID=wrong\x00", ""},
		{"CODEX_THREAD_ID=\x00", ""},
	} {
		if got := codexSessionEnvironment([]byte(tc.input)); got != tc.want {
			t.Fatalf("session = %q, want %q", got, tc.want)
		}
	}
}

func TestTaggedRootRejectsChangedIdentity(t *testing.T) {
	s := New()
	k := key("codex", "tagged")
	s.Begin(k, "e", Capabilities{})
	old := proc(100, 1, "old")
	reused := proc(100, 1, "new")
	s.Observe(k, "e", []Process{reused}, []Root{{PID: 100, TaskID: "tag", Identity: old.Identity, Evidence: "session-tag"}}, nil)
	if len(live(s.Read(k))) != 0 {
		t.Fatal("PID reused while reading marker inherited ownership")
	}
	s.Observe(k, "e", []Process{reused}, []Root{{PID: 100, TaskID: "new-tag", Identity: reused.Identity, Evidence: "session-tag"}}, nil)
	if live(s.Read(k))[100].Evidence != "session-tag" {
		t.Fatal("valid marker was not recorded")
	}
}
