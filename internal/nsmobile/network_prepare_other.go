//go:build !ios

package nsmobile

import "context"

func prepareSystemNetwork(ctx context.Context, _ string) error { return ctx.Err() }
