package desktopui

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/async"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

type remoteImageResult struct {
	done  chan struct{}
	image ui.ImageSource
	err   error
}

// Namespaced by server identity; the same absolute path on two machines
// must never share an image. Only thumbnails enter the client memory cache.
var desktopRemoteImages sync.Map

func (app *App) remoteImage(src string) *remoteImageResult {
	c := app.C
	return remoteImageFor(c.State().Remote, c.State().ReferenceSource, src)
}

func remoteImageFor(cl *svcproto.Client, source, src string) *remoteImageResult {
	key := source + "\x00" + src
	next := &remoteImageResult{done: make(chan struct{})}
	actual, loaded := desktopRemoteImages.LoadOrStore(key, next)
	entry := actual.(*remoteImageResult)
	if !loaded {
		go func() {
			var data []byte
			entry.err = cl.Call("host", svcproto.HostRequest{Action: "preview", Path: src}, &data)
			if entry.err == nil {
				entry.image, entry.err = ui.DecodeImage(data)
			}
			close(entry.done)
			if entry.err != nil {
				time.AfterFunc(5*time.Second, func() { desktopRemoteImages.CompareAndDelete(key, entry); uiruntime.Invalidate() })
			}
			uiruntime.Invalidate()
		}()
	}
	return entry
}

func (app *App) remoteMessageImage(src string) ui.ImageSource {
	if strings.HasPrefix(src, "data:") {
		return widgets.CachedImage(src)
	}
	entry := app.remoteImage(src)
	select {
	case <-entry.done:
		return entry.image
	default:
		return nil
	}
}

func (app *App) loadRemoteImage(src string) (ui.ImageSource, error) {
	if strings.HasPrefix(src, "data:") {
		return widgets.LoadImage(src)
	}
	c := app.C
	type imageHost struct {
		client *svcproto.Client
		source string
	}
	host, ok := async.OnMain(c.State().Ctx, c.Dispatch, func() imageHost { return imageHost{c.State().Remote, c.State().ReferenceSource} })
	if !ok {
		return nil, c.State().Ctx.Err()
	}
	entry := remoteImageFor(host.client, host.source, src)
	<-entry.done
	return entry.image, entry.err
}

func serverAttachmentSource(path string) string {
	return (&url.URL{Scheme: "file", Path: strings.TrimPrefix(path, "miya-server:")}).String()
}
