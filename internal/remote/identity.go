package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

type savedIdentity struct {
	Version      int                  `json:"version"`
	Key          key.NodePrivate      `json:"key"`
	PresharedKey tailcat.PresharedKey `json:"preshared_key"`
	Region       *tailcfg.DERPRegion  `json:"region"`
}

func identityPath(dir string) string { return filepath.Join(dir, "remote", "identity.json") }

// Hold this lock for the bridge's lifetime: two servers with the same node
// identity would compete for the relay and make both phones unreliable.
func lockIdentity(ctx context.Context, dir string) (*os.File, error) {
	parent := filepath.Dir(identityPath(dir))
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(parent, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(parent, "identity.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return f, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func releaseIdentity(f *os.File) {
	if f != nil {
		f.Close() // Releases the lock, including on process exit or crash.
	}
}

func loadIdentity(ctx context.Context, dir string) (savedIdentity, *os.File, error) {
	lock, err := lockIdentity(ctx, dir)
	if err != nil {
		return savedIdentity{}, nil, err
	}
	success := false
	defer func() {
		if !success {
			releaseIdentity(lock)
		}
	}()
	f, err := os.Open(identityPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		identity := tailcat.NewPrivateKey()
		success = true
		return savedIdentity{Version: 1, Key: identity.Private, PresharedKey: identity.Public.PresharedKey}, lock, nil
	}
	if err != nil {
		return savedIdentity{}, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return savedIdentity{}, nil, err
	}
	var identity savedIdentity
	if len(data) > 65536 || json.Unmarshal(data, &identity) != nil || identity.Version != 1 || identity.Key.IsZero() || identity.PresharedKey.IsZero() || identity.Region == nil || identity.Region.RegionID <= 0 || len(identity.Region.Nodes) == 0 {
		return savedIdentity{}, nil, errors.New("Saved connection identity is invalid. Reset the mobile connection.")
	}
	if err := f.Chmod(0600); err != nil {
		return savedIdentity{}, nil, err
	}
	success = true
	return identity, lock, nil
}

// Saving the selected relay as well as both keys keeps the complete QR code
// stable; selecting a different relay would strand phones holding the old one.
func saveIdentity(dir string, server *tailcat.Server) error {
	ci, err := tailcat.ParseAddr(server.TailcatAddr())
	if err != nil || len(ci.Region) != 1 {
		return errors.New("Unable to save connection identity")
	}
	data, err := json.Marshal(savedIdentity{Version: 1, Key: server.Key, PresharedKey: ci.PresharedKey, Region: ci.Region[0]})
	if err != nil {
		return errors.New("Unable to save connection identity")
	}
	return writeFile(identityPath(dir), data)
}

// ForgetIdentity revokes the current QR code. Call it only after closing the
// bridge; normal shutdown keeps the identity for the next launch.
func ForgetIdentity(ctx context.Context, dir string) error {
	lock, err := lockIdentity(ctx, dir)
	if err != nil {
		return err
	}
	defer releaseIdentity(lock)
	err = os.Remove(identityPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// writeFile replaces path atomically with a private file.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
