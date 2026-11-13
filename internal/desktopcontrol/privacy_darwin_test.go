//go:build darwin && !ios

package desktopcontrol

import (
	"reflect"
	"testing"
	"unsafe"
)

// A failed second display must restore both saved tables, using the returned
// sample count rather than the capacity. No physical display is touched.
func TestPrivacyRestoresAfterPartialStartup(t *testing.T) {
	if err := loadPrivacy(); err != nil {
		t.Fatal(err)
	}
	oldList, oldCap, oldGet, oldSet, oldOnline, oldRestore := getOnlineDisplays, gammaCapacity, getGamma, setGamma, displayOnline, restoreColorSync
	t.Cleanup(func() {
		getOnlineDisplays, gammaCapacity, getGamma, setGamma, displayOnline, restoreColorSync = oldList, oldCap, oldGet, oldSet, oldOnline, oldRestore
	})
	getOnlineDisplays = func(_ uint32, p *uint32, n *uint32) int32 {
		copy(unsafe.Slice(p, 2), []uint32{11, 22})
		*n = 2
		return 0
	}
	gammaCapacity = func(uint32) uint32 { return 4 }
	getGamma = func(_ uint32, cap uint32, r, g, b *float32, n *uint32) int32 {
		if cap == 256 {
			*n = 256
			return 0
		}
		copy(unsafe.Slice(r, 4), []float32{0.1, 0.2})
		copy(unsafe.Slice(g, 4), []float32{0.3, 0.4})
		copy(unsafe.Slice(b, 4), []float32{0.5, 0.6})
		*n = 2
		return 0
	}
	restored := map[uint32]bool{}
	setGamma = func(id, n uint32, r, g, b *float32) int32 {
		if n == 256 {
			if id == 22 {
				return 1
			}
			return 0
		}
		if n != 2 || !reflect.DeepEqual(unsafe.Slice(r, n), []float32{0.1, 0.2}) || !reflect.DeepEqual(unsafe.Slice(g, n), []float32{0.3, 0.4}) || !reflect.DeepEqual(unsafe.Slice(b, n), []float32{0.5, 0.6}) {
			t.Error("wrong restored table")
		}
		restored[id] = true
		return 0
	}
	displayOnline = func(uint32) bool { return true }
	restoreColorSync = func() { t.Error("unexpected ColorSync fallback") }
	var p privacyState
	if p.sync() {
		t.Fatal("accepted unsupported second display")
	}
	p.close()
	if len(restored) != 2 || !restored[11] || !restored[22] {
		t.Fatalf("restored: %v", restored)
	}
}

func TestPrivacyRestoreFallsBack(t *testing.T) {
	if err := loadPrivacy(); err != nil {
		t.Fatal(err)
	}
	oldSet, oldOnline, oldRestore := setGamma, displayOnline, restoreColorSync
	t.Cleanup(func() { setGamma, displayOnline, restoreColorSync = oldSet, oldOnline, oldRestore })
	setGamma = func(uint32, uint32, *float32, *float32, *float32) int32 { return 1 }
	displayOnline = func(uint32) bool { return true }
	fallback := false
	restoreColorSync = func() { fallback = true }
	p := privacyState{tables: []gammaTable{{id: 11, r: []float32{1}, g: []float32{1}, b: []float32{1}}}}
	p.close()
	if !fallback {
		t.Fatal("missing ColorSync fallback")
	}
}
