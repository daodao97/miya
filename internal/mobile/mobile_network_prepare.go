package mobile

import (
	"context"
	"errors"

	"github.com/egoist/mygo/plugins/network"

	"miya/internal/remote"
)

// The phone prepares iOS network access through MyGo before remote opens
// its tunnel; network.Prepare returns at once on desktops.
func init() {
	remote.PrepareNetwork = func(ctx context.Context, rawURL string) error {
		err := network.Prepare(ctx, rawURL)
		var failure *network.Error
		if errors.As(err, &failure) {
			return &remote.PrepareError{Kind: failure.Kind, Err: err}
		}
		return err
	}
}
