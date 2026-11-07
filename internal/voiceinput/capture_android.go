//go:build android && cgo

package voiceinput

import (
	"context"
	"errors"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/android/jni"
)

type androidRecording struct{ stop, cancel func() }

func (r androidRecording) Stop()   { r.stop() }
func (r androidRecording) Cancel() { r.cancel() }
func startAudio(ctx context.Context, emit func([]byte, error, bool)) (Recording, error) {
	status, err := mygo.Permissions.Request(mygo.PermissionMicrophone)
	if err != nil {
		return nil, err
	}
	if status != mygo.PermissionGranted {
		return nil, errors.New("microphone permission denied")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stop, cancel := jni.BeginAudio(emit, func(err error) {
		if err != nil {
			emit(nil, err, true)
		}
	})
	r := androidRecording{stop: stop, cancel: cancel}
	if err := ctx.Err(); err != nil {
		r.Cancel()
		return nil, err
	}
	return r, nil
}
