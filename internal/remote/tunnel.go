package remote

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// A Tailcat server keeps every node key that ever reached it as a WireGuard
// peer. A fresh key per tunnel would grow that list with every connection, so
// each phone derives one stable key per desktop from a private secret, as
// Retty does.
var clientSecret atomic.Pointer[[]byte]

// SetClientSecret sets the private seed of this device's tunnel keys. Without
// one every tunnel uses an ephemeral key.
func SetClientSecret(secret []byte) {
	if len(secret) == 0 {
		clientSecret.Store(nil)
		return
	}
	secret = append([]byte(nil), secret...)
	clientSecret.Store(&secret)
}

func clientKey(addr tailcat.Addr, slot byte) key.NodePrivate {
	secret := clientSecret.Load()
	ci, err := tailcat.ParseAddr(addr)
	if secret == nil || err != nil {
		return key.NodePrivate{}
	}
	mac := hmac.New(sha256.New, *secret)
	mac.Write([]byte("miya tailcat client v1\x00"))
	mac.Write(ci.ServerPublic.NodePublic.AppendTo(nil))
	mac.Write([]byte{slot})
	raw := mac.Sum(nil)
	raw[0] &= 248
	raw[31] = raw[31]&127 | 64
	var k key.NodePrivate
	if k.UnmarshalText([]byte("privkey:"+hex.EncodeToString(raw))) != nil {
		return key.NodePrivate{}
	}
	return k
}

// The desktop trusts a key's last direct path for a few seconds after its
// last pong; a new tunnel under a key closed moments ago would wait out
// WireGuard's retry. Each desktop therefore gets a few key slots, and a
// tunnel takes the first one neither live nor closed within keyRest.
const keySlots = 4
const keyRest = 10 * time.Second

var keyed struct {
	sync.Mutex
	live   map[key.NodePublic]bool
	closed map[key.NodePublic]time.Time
	first  byte
	path   string
	saved  int
}

// SetKeySlotFile keeps the last claimed slot in path, so a process that iOS
// ended without closing its tunnels does not reuse a slot the desktop still
// routes to a dead socket.
func SetKeySlotFile(path string) {
	keyed.Lock()
	defer keyed.Unlock()
	keyed.path, keyed.first, keyed.saved = path, 0, -1
	if data, err := os.ReadFile(path); err == nil && len(data) == 1 && data[0] < keySlots {
		keyed.first, keyed.saved = (data[0]+1)%keySlots, int(data[0])
	}
}

func claimKey(addr tailcat.Addr) key.NodePrivate {
	keyed.Lock()
	defer keyed.Unlock()
	for i := range byte(keySlots) {
		slot := (keyed.first + i) % keySlots
		k := clientKey(addr, slot)
		if k.IsZero() {
			return k
		}
		pub := k.Public()
		if keyed.live[pub] || time.Since(keyed.closed[pub]) < keyRest {
			continue
		}
		if keyed.live == nil {
			keyed.live = make(map[key.NodePublic]bool)
		}
		keyed.live[pub] = true
		if keyed.path != "" && int(slot) != keyed.saved && writeFile(keyed.path, []byte{slot}) == nil {
			keyed.saved = int(slot)
		}
		return k
	}
	return key.NodePrivate{}
}

type tunnel struct {
	client *tailcat.Client
	pub    key.NodePublic // zero for an ephemeral key
	used   atomic.Bool
	once   sync.Once
}

func openTunnel(addr, server tailcat.Addr) *tunnel {
	t := &tunnel{client: &tailcat.Client{Server: server, Logf: quiet}}
	if k := claimKey(addr); !k.IsZero() {
		t.client.Key, t.pub = k, k.Public()
	}
	return t
}

// close releases the tunnel's key at once, then drains and shuts it down in
// the background: the final FIN must leave before the userspace TCP stack
// stops.
func (t *tunnel) close() {
	t.once.Do(func() {
		if !t.pub.IsZero() {
			keyed.Lock()
			delete(keyed.live, t.pub)
			if keyed.closed == nil {
				keyed.closed = make(map[key.NodePublic]time.Time)
			}
			keyed.closed[t.pub] = time.Now()
			keyed.Unlock()
		}
		go func() {
			if t.used.Load() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				t.client.DrainTCP(ctx)
				cancel()
			}
			t.client.Close()
		}()
	})
}
