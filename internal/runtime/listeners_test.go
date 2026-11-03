package runtime

import "testing"

func TestListenersKeepAddressAndProcessIdentity(t *testing.T) {
	identity := proc(10, 1, "start").Identity
	raw := []byte("p10\x00cnode\x00\nf1\x00tIPv4\x00n*:3000\x00\nf2\x00tIPv6\x00n*:3000\x00\nf3\x00tIPv4\x00n192.168.0.1:4000\x00\np20\x00cother\x00\nf1\x00tIPv4\x00n*:5000\x00\n")
	endpoints := ParseListeners(raw, map[int]Identity{10: identity})
	if len(endpoints) != 2 || endpoints[0].Host != "127.0.0.1" || endpoints[1].Host != "::1" || endpoints[0].Process != identity || endpoints[1].Process != identity {
		t.Fatalf("address or ownership lost: %+v", endpoints)
	}
}
