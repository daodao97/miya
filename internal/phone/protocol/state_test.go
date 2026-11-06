package protocol_test

import (
	"encoding/json"
	"testing"

	phonecache "miya/internal/phone/cache"
	phoneproto "miya/internal/phone/protocol"
)

// Old phone state and disk caches use the same project field names, including
// explicit false and null values. Moving the type must preserve that contract.
func TestProjectSnapshotWireAndCacheCompatibility(t *testing.T) {
	const projects = `[{"name":"Miya","path":"/workspace/Miya","current":true,"threads":[]},{"name":"Other","path":"/other","current":false,"threads":null}]`
	var state phoneproto.State
	if err := json.Unmarshal([]byte(`{"projects":`+projects+`}`), &state); err != nil {
		t.Fatal(err)
	}
	saved, err := json.Marshal(phonecache.SavedState{Host: "Desktop", State: state})
	if err != nil {
		t.Fatal(err)
	}
	var restored phonecache.SavedState
	if err := json.Unmarshal(saved, &restored); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(restored.State.Projects)
	if err != nil || string(got) != projects || restored.Host != "Desktop" {
		t.Fatalf("project cache contract changed: %s, host %q, error %v", got, restored.Host, err)
	}
}
