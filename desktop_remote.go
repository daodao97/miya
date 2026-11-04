package main

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

func remoteServerPath() string { return filepath.Join(serviceDir(), "desktop-workspaces.json") }

func remoteServerLink() string {
	store, err := loadDesktopWorkspaces()
	if err != nil {
		return ""
	}
	return store.current().Link
}

func saveRemoteServer(link string) error {
	link = strings.TrimSpace(link)
	if link != "" {
		if _, err := remote.ParseLink(link); err != nil {
			return err
		}
	}
	store, err := loadDesktopWorkspaces()
	if err != nil {
		return err
	}
	store.Active = localDesktopWorkspace
	if link != "" {
		item, err := store.remember(link, "")
		if err != nil {
			return err
		}
		store.Active = item.ID
	}
	return store.save()
}

func connectRemoteService(link string) (*serviceClient, svcState, error) {
	return connectRemoteServiceContext(context.Background(), link)
}

func connectRemoteServiceContext(parent context.Context, link string) (*serviceClient, svcState, error) {
	if err := prepareDesktopIdentity(); err != nil {
		return nil, svcState{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	wire, err := remote.ConnectService(ctx, link)
	if err != nil {
		return nil, svcState{}, err
	}
	cl := &serviceClient{wire: wire, pending: map[int64]chan svcMessage{}, closed: make(chan struct{})}
	stopCancel := context.AfterFunc(ctx, cl.Close)
	defer stopCancel()
	go cl.read()
	var hello svcHello
	if err = cl.call("hello", nil, &hello); err == nil && hello.Version != serviceVersion {
		err = errors.New("Server protocol version is incompatible. Update both the Miya desktop client and server.")
	}
	var state svcState
	if err == nil {
		err = cl.call("state", nil, &state)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cl.Close()
		return nil, svcState{}, err
	}
	// A client never compares executable paths, replaces, or starts a remote
	// service. Deployment and process supervision belong to the server.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-cl.closed:
				return
			case <-ticker.C:
				if cl.call("hello", nil, nil) != nil {
					cl.Close()
					return
				}
			}
		}
	}()
	return cl, state, nil
}

var desktopIdentity struct {
	sync.Once
	err error
}

func prepareDesktopIdentity() error {
	desktopIdentity.Do(func() {
		if err := os.MkdirAll(serviceDir(), 0700); err != nil {
			desktopIdentity.err = err
			return
		}
		path := filepath.Join(serviceDir(), "desktop-client-secret")
		secret, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			secret = make([]byte, 32)
			if _, err = rand.Read(secret); err == nil {
				err = writePrivateFile(path, secret)
			}
		}
		if err == nil && len(secret) != 32 {
			err = errors.New("Invalid desktop connection identity file")
		}
		if err != nil {
			desktopIdentity.err = err
			return
		}
		remote.SetClientSecret(secret)
		remote.SetKeySlotFile(filepath.Join(serviceDir(), "desktop-client-key-slot"))
	})
	return desktopIdentity.err
}

func (c *chatController) remoteHost() bool {
	return c != nil && c.remote != nil && (c.remote.wire != nil || c.mirror != nil && c.mirror.remoteHost)
}

func (app *chatApp) browseServerProject(path string) {
	if app.projectBrowseLoading {
		return
	}
	app.projectBrowse, app.projectBrowseLoading, app.projectError = true, true, ""
	cl := app.c.remote
	go func() {
		var dirs []hostEntry
		err := cl.call("host", hostRequest{Action: "browse", Path: path}, &dirs)
		app.c.dispatch(func() {
			app.projectBrowseLoading = false
			if err != nil {
				app.projectError = err.Error()
				return
			}
			app.projectDirectories = dirs
		})
	}()
}

func (app *chatApp) serverProjectFolders(c *ui.Context) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Server folders").FontSize(12).Grow(1)
		if ui.Button(c.Key("server-parent"), "Parent folder").Disabled(app.projectBrowseLoading).Clicked() {
			app.projectPath = filepath.Dir(app.projectPath)
			app.browseServerProject(app.projectPath)
		}
	})
	if app.projectBrowseLoading {
		ui.Spinner(c)
		return
	}
	ui.Scroll(c.Key("server-folders")).FillWidth().MaxHeight(180).Children(func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			for _, dir := range app.projectDirectories {
				if ui.Button(c.Key(dir.Path), dir.Name).FillWidth().Clicked() {
					app.projectPath = dir.Path
					app.browseServerProject(dir.Path)
				}
			}
		})
	})
}
