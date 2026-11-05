//go:build darwin && !ios && cgo

package desktop

/*
#include <stdlib.h>
int miya_desktop_displays(char **json);
int miya_desktop_capture_display(unsigned int display, const char *uuid, void **bytes, int *size, char **failure);
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

func Displays() ([]Display, error) {
	var ptr *C.char
	if err := permissionError(int(C.miya_desktop_displays(&ptr))); err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(ptr))
	var displays []Display
	err := json.Unmarshal([]byte(C.GoString(ptr)), &displays)
	return displays, err
}

func CaptureDisplay(display Display) ([]byte, error) {
	if display.ID == 0 || display.UUID == "" {
		return nil, ErrDisplayUnavailable
	}
	uuid := C.CString(display.UUID)
	defer C.free(unsafe.Pointer(uuid))
	var ptr unsafe.Pointer
	var size C.int
	var failure *C.char
	code := int(C.miya_desktop_capture_display(C.uint(display.ID), uuid, &ptr, &size, &failure))
	if failure != nil {
		defer C.free(unsafe.Pointer(failure))
	}
	if err := captureError(code, C.GoString(failure)); err != nil {
		return nil, err
	}
	defer C.free(ptr)
	return C.GoBytes(ptr, size), nil
}

func SendDisplayInput(display Display, in Input) error {
	if display.ID == 0 || display.UUID == "" {
		return ErrDisplayUnavailable
	}
	return sendInput(Window{}, display, in)
}
