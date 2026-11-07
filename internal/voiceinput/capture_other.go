//go:build !ios || !cgo

package voiceinput

import (
	"context"
	"errors"
)

func startAudio(context.Context, func([]byte, error, bool)) (Recording, error) {
	return nil, errors.New("microphone capture is not available on this platform")
}
