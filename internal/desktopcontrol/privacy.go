package desktopcontrol

import "context"

// RunPrivacy blacks physical displays through their gamma tables until ctx
// ends or Control+Option+Command+P is pressed locally. It restores the original
// tables on every return, including startup failure and display changes.
//
// Run it on the main thread of a dedicated helper process, never in an app
// process: the helper must have an independent lifetime and cancellation on
// loss of its parent's heartbeat. ready is called after all displays are black
// and the recovery shortcut is installed. A ready error also restores them.
func RunPrivacy(ctx context.Context, ready func() error) error { return runPrivacy(ctx, ready) }
