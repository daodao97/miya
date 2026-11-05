//go:build darwin && !ios && cgo

package desktop

/*
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices -framework ScreenCaptureKit -framework ImageIO -framework IOKit
#include <stdlib.h>
int miya_desktop_available(void);
int miya_desktop_authorize(void);
int miya_desktop_wake(void);
int miya_desktop_locked(void);
int miya_desktop_capture(void **bytes, int *size, char **failure);
int miya_desktop_windows(char **json);
int miya_desktop_capture_window(unsigned int window, int pid, void **bytes, int *size, char **failure);
int miya_desktop_window_input(unsigned int window, int pid, int kind, double x, double y, double tx, double ty, int dx, int dy, const unsigned short *text, int length, int key, int modifier);
int miya_desktop_dialog(unsigned int window, int pid, char **json);
int miya_desktop_press(unsigned int window, int pid, const char *label);
int miya_desktop_input(int kind, double x, double y, double tx, double ty, int dx, int dy, const unsigned short *text, int length, int key, int modifier);
int miya_desktop_display_input(unsigned int display, const char *uuid, int kind, double x, double y, double tx, double ty, int dx, int dy, const unsigned short *text, int length, int key, int modifier);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"unicode/utf16"
	"unsafe"
)

func Available() bool { return C.miya_desktop_available() != 0 }

func Authorize() error { return permissionError(int(C.miya_desktop_authorize())) }

// ScreenLocked reports whether the console session shows the lock screen.
func ScreenLocked() (bool, error) {
	switch C.miya_desktop_locked() {
	case 1:
		return true, nil
	case 0:
		return false, nil
	}
	return false, errors.New("Unable to read Mac lock status")
}

// WakeDisplay declares local user activity so a sleeping display shows the
// desktop or lock screen again before capture.
func WakeDisplay() error { return permissionError(int(C.miya_desktop_wake())) }

func Capture() ([]byte, error) {
	var ptr unsafe.Pointer
	var size C.int
	var failure *C.char
	code := int(C.miya_desktop_capture(&ptr, &size, &failure))
	if failure != nil {
		defer C.free(unsafe.Pointer(failure))
	}
	if err := captureError(code, C.GoString(failure)); err != nil {
		return nil, err
	}
	defer C.free(ptr)
	return C.GoBytes(ptr, size), nil
}

// Windows checks existing recording permission without opening a TCC prompt.
func Windows() ([]Window, error) {
	var ptr *C.char
	if err := permissionError(int(C.miya_desktop_windows(&ptr))); err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(ptr))
	var windows []Window
	err := json.Unmarshal([]byte(C.GoString(ptr)), &windows)
	return windows, err
}

func WindowDialog(window Window) (Dialog, error) {
	var ptr *C.char
	if err := permissionError(int(C.miya_desktop_dialog(C.uint(window.ID), C.int(window.PID), &ptr))); err != nil {
		return Dialog{}, err
	}
	defer C.free(unsafe.Pointer(ptr))
	var dialog Dialog
	err := json.Unmarshal([]byte(C.GoString(ptr)), &dialog)
	return dialog, err
}

func PressWindowAction(window Window, label string) error {
	if window.ID == 0 || window.PID <= 0 || label == "" || len(label) > 512 {
		return permissionError(4)
	}
	ptr := C.CString(label)
	defer C.free(unsafe.Pointer(ptr))
	return permissionError(int(C.miya_desktop_press(C.uint(window.ID), C.int(window.PID), ptr)))
}

func CaptureWindow(window Window) ([]byte, error) {
	if window.ID == 0 || window.PID <= 0 {
		return nil, permissionError(4)
	}
	var ptr unsafe.Pointer
	var size C.int
	var failure *C.char
	code := int(C.miya_desktop_capture_window(C.uint(window.ID), C.int(window.PID), &ptr, &size, &failure))
	if failure != nil {
		defer C.free(unsafe.Pointer(failure))
	}
	if err := captureError(code, C.GoString(failure)); err != nil {
		return nil, err
	}
	defer C.free(ptr)
	return C.GoBytes(ptr, size), nil
}

func SendInput(in Input) error {
	return sendInput(Window{}, Display{}, in)
}

func SendWindowInput(window Window, in Input) error {
	if window.ID == 0 || window.PID <= 0 {
		return permissionError(4)
	}
	return sendInput(window, Display{}, in)
}

func sendInput(window Window, display Display, in Input) error {
	if err := in.Validate(); err != nil {
		return err
	}
	kinds := map[string]int{"click": 0, "doubleClick": 1, "rightClick": 2, "drag": 3, "scroll": 4, "text": 5, "key": 6, "move": 7}
	keys := map[string]int{"enter": 36, "tab": 48, "escape": 53, "backspace": 51, "delete": 117, "left": 123, "right": 124, "up": 126, "down": 125, "a": 0, "c": 8, "v": 9, "z": 6, "l": 37}
	mods := map[string]int{"command": 1, "control": 2, "shift": 3, "option": 4}
	units := utf16.Encode([]rune(in.Text))
	var ptr *C.ushort
	if len(units) > 0 {
		ptr = (*C.ushort)(unsafe.Pointer(&units[0]))
	}
	if window.ID != 0 {
		return permissionError(int(C.miya_desktop_window_input(C.uint(window.ID), C.int(window.PID), C.int(kinds[in.Kind]), C.double(in.X), C.double(in.Y), C.double(in.ToX), C.double(in.ToY), C.int(in.DeltaX), C.int(in.DeltaY), ptr, C.int(len(units)), C.int(keys[in.Key]), C.int(mods[in.Modifier]))))
	}
	if display.ID != 0 {
		uuid := C.CString(display.UUID)
		defer C.free(unsafe.Pointer(uuid))
		return permissionError(int(C.miya_desktop_display_input(C.uint(display.ID), uuid, C.int(kinds[in.Kind]), C.double(in.X), C.double(in.Y), C.double(in.ToX), C.double(in.ToY), C.int(in.DeltaX), C.int(in.DeltaY), ptr, C.int(len(units)), C.int(keys[in.Key]), C.int(mods[in.Modifier]))))
	}
	return permissionError(int(C.miya_desktop_input(C.int(kinds[in.Kind]), C.double(in.X), C.double(in.Y), C.double(in.ToX), C.double(in.ToY), C.int(in.DeltaX), C.int(in.DeltaY), ptr, C.int(len(units)), C.int(keys[in.Key]), C.int(mods[in.Modifier]))))
}
