// Package miyanative is Miya's own native code on phones, as a MyGo plugin
// (mygo-plugin.json): the sheet that shows a desktop's project files and web
// services on iOS, and the entitlements the app signs with.
package miyanative

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/egoist/mygo"
)

const pluginID = "miya"

var ids atomic.Int64

// OpenFiles shows rawURL, the phone's file proxy, in a sheet with a web view
// that downloads, previews and shares files, and returns as the user closes
// it or ctx ends. It reports errors.ErrUnsupported where the app has no such
// sheet (Android, desktops): show the page in the browser there.
func OpenFiles(ctx context.Context, rawURL string) error {
	p := mygo.Native(pluginID)
	if !p.Available() {
		return errors.ErrUnsupported
	}
	id := ids.Add(1)
	err := p.Call(ctx, "openFiles", map[string]any{"id": id, "url": rawURL}, nil)
	if ctx.Err() != nil {
		go p.Call(context.Background(), "closeFiles", map[string]any{"id": id}, nil)
		return ctx.Err()
	}
	var n *mygo.NativeError
	if errors.As(err, &n) && (n.Code == "NoPlugin" || n.Code == "NoMethod") {
		return errors.ErrUnsupported
	}
	return err
}
