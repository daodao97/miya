package main

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"miya/internal/async"

	"github.com/egoist/mygo/ui"
)

type remoteImageResult struct {
	done  chan struct{}
	image ui.ImageSource
	err   error
}

// Namespaced by server identity; the same absolute path on two machines
// must never share an image. Only thumbnails enter the client memory cache.
var desktopRemoteImages sync.Map

func (app *chatApp) remoteImage(src string) *remoteImageResult {
	c := app.c
	return remoteImageFor(c.remote, c.referenceSource, src)
}

func remoteImageFor(cl *serviceClient, source, src string) *remoteImageResult {
	key := source + "\x00" + src
	next := &remoteImageResult{done: make(chan struct{})}
	actual, loaded := desktopRemoteImages.LoadOrStore(key, next)
	entry := actual.(*remoteImageResult)
	if !loaded {
		go func() {
			var data []byte
			entry.err = cl.Call("host", hostRequest{Action: "preview", Path: src}, &data)
			if entry.err == nil {
				entry.image, entry.err = ui.DecodeImage(data)
			}
			close(entry.done)
			if entry.err != nil {
				time.AfterFunc(5*time.Second, func() { desktopRemoteImages.CompareAndDelete(key, entry); invalidate() })
			}
			invalidate()
		}()
	}
	return entry
}

func (app *chatApp) remoteMessageImage(src string) ui.ImageSource {
	if strings.HasPrefix(src, "data:") {
		return cachedImage(src)
	}
	entry := app.remoteImage(src)
	select {
	case <-entry.done:
		return entry.image
	default:
		return nil
	}
}

func (app *chatApp) loadRemoteImage(src string) (ui.ImageSource, error) {
	if strings.HasPrefix(src, "data:") {
		return loadImage(src)
	}
	c := app.c
	type imageHost struct {
		client *serviceClient
		source string
	}
	host, ok := async.OnMain(c.ctx, c.dispatch, func() imageHost { return imageHost{c.remote, c.referenceSource} })
	if !ok {
		return nil, c.ctx.Err()
	}
	entry := remoteImageFor(host.client, host.source, src)
	<-entry.done
	return entry.image, entry.err
}

func serverAttachmentSource(path string) string {
	return (&url.URL{Scheme: "file", Path: strings.TrimPrefix(path, "miya-server:")}).String()
}
