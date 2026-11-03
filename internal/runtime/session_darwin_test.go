package runtime

import (
	"encoding/binary"
	"testing"
)

func TestProcargsSkipsArgumentsBeforeSessionEnvironment(t *testing.T) {
	data := make([]byte, 4)
	binary.NativeEndian.PutUint32(data, 3)
	data = append(data, []byte("/bin/service\x00\x00/bin/service\x00CODEX_THREAD_ID=argument\x00\x00SECRET=hidden\x00CODEX_THREAD_ID=real-session\x00")...)
	if got := codexSessionProcargs(data); got != "real-session" {
		t.Fatalf("session = %q", got)
	}
	if got := codexSessionProcargs(data[:8]); got != "" {
		t.Fatal("accepted truncated process arguments")
	}
}
