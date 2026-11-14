package protocol

import (
	"strings"
	"testing"

	"miya/internal/chat/model"
)

func TestActivityURLRoundTripAndValidation(t *testing.T) {
	ref := model.ThreadRef{Source: strings.Repeat("a", 32), Backend: "codex", SessionID: "session/with spaces"}.URL()
	desktop := strings.Repeat("b", 24)
	raw := ActivityURL(ref, desktop)
	got, id, ok := ParseActivityURL(raw)
	if !ok || got != ref || id != desktop {
		t.Fatal("activity URL lost routing", raw)
	}
	if ActivityURL(ref, "") != ref {
		t.Fatal("plain reference compatibility lost")
	}
	for _, bad := range []string{raw + "&desktop=duplicate", raw + "&extra=1", raw + "#fragment", "miya://notification?desktop=no&reference=no"} {
		if _, _, ok := ParseActivityURL(bad); ok {
			t.Fatal("invalid activity URL accepted", bad)
		}
	}
}
