//go:build ios && cgo

package voiceinput

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AVFoundation
#include <stdint.h>
void miya_voice_start(uint64_t token);
void miya_voice_stop(uint64_t token, int cancel);
*/
import "C"

import (
	"context"
	"errors"
	"sync"
	"unsafe"

	"github.com/egoist/mygo"
)

var captures = struct {
	sync.Mutex
	next      uint64
	callbacks map[uint64]func([]byte, error, bool)
}{callbacks: map[uint64]func([]byte, error, bool){}}

type nativeRecording struct{ token uint64 }

func (r nativeRecording) Stop() { C.miya_voice_stop(C.uint64_t(r.token), 0) }
func (r nativeRecording) Cancel() {
	C.miya_voice_stop(C.uint64_t(r.token), 1)
	captures.Lock()
	delete(captures.callbacks, r.token)
	captures.Unlock()
}

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
	captures.Lock()
	captures.next++
	token := captures.next
	captures.callbacks[token] = emit
	captures.Unlock()
	C.miya_voice_start(C.uint64_t(token))
	r := nativeRecording{token}
	if err := ctx.Err(); err != nil {
		r.Cancel()
		return nil, err
	}
	return r, nil
}

//export miyaVoiceAudio
func miyaVoiceAudio(token C.uint64_t, data unsafe.Pointer, size C.int, failure *C.char, end C.int) {
	captures.Lock()
	callback := captures.callbacks[uint64(token)]
	if end != 0 {
		delete(captures.callbacks, uint64(token))
	}
	captures.Unlock()
	if callback == nil {
		return
	}
	var err error
	if failure != nil {
		err = errors.New(C.GoString(failure))
	}
	var b []byte
	if size > 0 {
		b = C.GoBytes(data, size)
	}
	callback(b, err, end != 0)
}
