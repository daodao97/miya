// Package workspaces keeps the desktop's workspaces, this Mac and the remote
// servers it has paired with, and connects a window to a server's service.
package workspaces

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"miya/internal/appdir"
	"miya/internal/fsutil"
	"miya/internal/remote"
	"miya/internal/svcproto"
)

func ConnectRemoteService(link string) (*svcproto.Client, svcproto.State, error) {
	return ConnectRemoteServiceContext(context.Background(), link)
}

func ConnectRemoteServiceContext(parent context.Context, link string) (*svcproto.Client, svcproto.State, error) {
	if err := PrepareIdentity(); err != nil {
		return nil, svcproto.State{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	wire, err := remote.ConnectService(ctx, link)
	if err != nil {
		return nil, svcproto.State{}, err
	}
	cl := svcproto.NewWireClient(wire)
	stopCancel := context.AfterFunc(ctx, cl.Close)
	defer stopCancel()
	var hello svcproto.Hello
	if err = cl.Call("hello", nil, &hello); err == nil && hello.Version != svcproto.Version {
		err = errors.New("Server protocol version is incompatible. Update both the Miya desktop client and server.")
	}
	var state svcproto.State
	if err == nil {
		err = cl.Call("state", nil, &state)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cl.Close()
		return nil, svcproto.State{}, err
	}
	// A client never compares executable paths, replaces, or starts a remote
	// service. Deployment and process supervision belong to the server.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-cl.Done():
				return
			case <-ticker.C:
				if cl.Call("hello", nil, nil) != nil {
					cl.Close()
					return
				}
			}
		}
	}()
	return cl, state, nil
}

var Identity struct {
	sync.Once
	err error
}

func PrepareIdentity() error {
	Identity.Do(func() {
		if err := os.MkdirAll(appdir.Dir(), 0700); err != nil {
			Identity.err = err
			return
		}
		path := filepath.Join(appdir.Dir(), "desktop-client-secret")
		secret, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			secret = make([]byte, 32)
			if _, err = rand.Read(secret); err == nil {
				err = fsutil.WritePrivateFile(path, secret)
			}
		}
		if err == nil && len(secret) != 32 {
			err = errors.New("Invalid desktop connection identity file")
		}
		if err != nil {
			Identity.err = err
			return
		}
		remote.SetClientSecret(secret)
		remote.SetKeySlotFile(filepath.Join(appdir.Dir(), "desktop-client-key-slot"))
	})
	return Identity.err
}

func RemoteServerLink() string {
	store, err := Load()
	if err != nil {
		return ""
	}
	return store.Current().Link
}

func RemoteServerPath() string { return filepath.Join(appdir.Dir(), "desktop-workspaces.json") }

func SaveRemoteServer(link string) error {
	link = strings.TrimSpace(link)
	if link != "" {
		if _, err := remote.ParseLink(link); err != nil {
			return err
		}
	}
	store, err := Load()
	if err != nil {
		return err
	}
	store.Active = Local
	if link != "" {
		item, err := store.Remember(link, "")
		if err != nil {
			return err
		}
		store.Active = item.ID
	}
	return store.Save()
}

func Defaults() Store {
	return Store{Version: 1, Active: Local, Items: []Workspace{{ID: Local, Name: "Local"}}}
}

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link,omitempty"`
}

type Store struct {
	Version int         `json:"version"`
	Active  string      `json:"active"`
	Items   []Workspace `json:"items"`
}

func (s Store) Clone() Store {
	s.Items = append([]Workspace(nil), s.Items...)
	return s
}

func (s Store) Current() Workspace {
	item, _ := s.Find(s.Active)
	return item
}

func (s Store) Find(id string) (Workspace, bool) {
	for _, item := range s.Items {
		if item.ID == id {
			return item, true
		}
	}
	return Workspace{}, false
}

func (s *Store) Remember(link, name string) (Workspace, error) {
	id, err := IDFromLink(link)
	if err != nil {
		return Workspace{}, err
	}
	id = "remote-" + id
	name = strings.TrimSpace(name)
	for i := range s.Items {
		if s.Items[i].ID == id {
			if name != "" {
				s.Items[i].Name = name
			}
			return s.Items[i], nil
		}
	}
	if name == "" {
		name = fmt.Sprintf("Remote workspace %d", len(s.Items))
	}
	item := Workspace{ID: id, Name: name, Link: strings.TrimSpace(link)}
	s.Items = append(s.Items, item)
	return item, nil
}

func (s Store) Save() error {
	if err := os.MkdirAll(appdir.Dir(), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return fsutil.WritePrivateFile(RemoteServerPath(), data)
}

func Load() (Store, error) {
	store := Defaults()
	data, err := os.ReadFile(RemoteServerPath())
	if err == nil {
		var saved Store
		if json.Unmarshal(data, &saved) != nil || saved.Version != 1 {
			return store, errors.New("Unable to read the workspace list")
		}
		for _, item := range saved.Items {
			if item.ID == Local {
				continue
			}
			remembered, err := store.Remember(item.Link, item.Name)
			if err != nil || remembered.ID != item.ID {
				return Defaults(), errors.New("The saved workspace connection is invalid")
			}
		}
		if _, ok := store.Find(saved.Active); ok {
			store.Active = saved.Active
		}
		return store, nil
	}
	if !os.IsNotExist(err) {
		return store, errors.New("Unable to read the workspace list")
	}
	// Migrate the old singleton in memory. The next successful selection
	// saves the registry; an intentional local selection never resurrects it.
	data, err = os.ReadFile(filepath.Join(appdir.Dir(), "remote-client.json"))
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return store, errors.New("Unable to read the previous server connection")
	}
	var legacy struct {
		Link string `json:"link"`
	}
	if json.Unmarshal(data, &legacy) != nil {
		return store, errors.New("The previous server connection is invalid")
	}
	if legacy.Link != "" {
		item, err := store.Remember(legacy.Link, "")
		if err != nil {
			return store, errors.New("The previous server connection is invalid")
		}
		store.Active = item.ID
	}
	return store, nil
}

const Local = "local"

func IDFromLink(raw string) (string, error) {
	addr, err := remote.ParseLink(raw)
	if err != nil {
		return "", err
	}
	return CacheName(string(addr)), nil
}

// cacheName names a file or directory without the value it stands for.
func CacheName(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:12])
}
