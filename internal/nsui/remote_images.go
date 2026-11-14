package nsui

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	svcproto "miya/internal/service/protocol"
)

type remoteImageResult struct {
	done chan struct{}
	data []byte
	err  error
}

var remoteImages sync.Map

// Only server thumbnails enter this cache, namespaced by stable server identity.
// Publishing through done makes waiting readers safe without reading UI state.
func remoteImageSource(cx *core.Scope, c Host, src string) func() string {
	if strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "http:") || strings.HasPrefix(src, "https:") {
		return func() string { return src }
	}
	if !c.RemoteHost() && !strings.HasPrefix(src, "miya-server:") {
		if !strings.HasPrefix(src, "file:") {
			src = (&url.URL{Scheme: "file", Path: src}).String()
		}
		return func() string { return src }
	}
	client, source := c.State().Remote, c.State().ReferenceSource
	if client == nil {
		return func() string { return "" }
	}
	if strings.HasPrefix(src, "miya-server:") {
		src = (&url.URL{Scheme: "file", Path: strings.TrimPrefix(src, "miya-server:")}).String()
	}
	if !strings.HasPrefix(src, "file:") {
		src = (&url.URL{Scheme: "file", Path: src}).String()
	}
	key := source + "\x00" + src
	result := core.NewSignal(cx, "")
	var load func()
	load = func() {
		next := &remoteImageResult{done: make(chan struct{})}
		value, loaded := remoteImages.LoadOrStore(key, next)
		entry := value.(*remoteImageResult)
		if !loaded {
			go func() {
				entry.err = client.Call("host", svcproto.HostRequest{Action: "preview", Path: src}, &entry.data)
				close(entry.done)
				if entry.err != nil {
					time.AfterFunc(5*time.Second, func() { remoteImages.CompareAndDelete(key, entry) })
				}
			}()
		}
		go func() {
			<-entry.done
			cx.Post(func() {
				if entry.err == nil {
					result.Set(imageDataURI(entry.data))
				} else {
					cx.AfterFunc(5*time.Second, load)
				}
			})
		}()
	}
	load()
	return result.Get
}
