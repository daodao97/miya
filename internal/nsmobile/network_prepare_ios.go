//go:build ios && cgo

package nsmobile

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
void *miya_network_start(const char *url);
int miya_network_result(void *request);
void miya_network_close(void *request);
*/
import "C"

import (
	"context"
	"errors"
	"time"
	"unsafe"
)

// BSD sockets alone do not activate all iOS cellular/local-network permission
// and VPN/DNS64 routes. A HEAD through URLSession prepares the system stack,
// like MyGo's network.Prepare. Any HTTP response is sufficient.
func prepareSystemNetwork(ctx context.Context, relay string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	url := C.CString(relay)
	defer C.free(unsafe.Pointer(url))
	request := C.miya_network_start(url)
	if request == nil {
		return errors.New(tr("Unable to reach the relay server"))
	}
	defer C.miya_network_close(request)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch C.miya_network_result(request) {
		case 1:
			return ctx.Err()
		case -1:
			return errors.New(tr("Unable to reach the relay server"))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
