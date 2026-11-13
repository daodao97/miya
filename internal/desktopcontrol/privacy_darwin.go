//go:build darwin && !ios

package desktopcontrol

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"structs"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"
)

type eventType struct {
	_           structs.HostLayout
	Class, Kind uint32
}
type hotKeyID struct {
	_             structs.HostLayout
	Signature, ID uint32
}

var (
	privacyOnce            sync.Once
	privacyLoadErr         error
	privacyActive          atomic.Bool
	privacyExit            atomic.Bool
	privacyHandler         uintptr
	getOnlineDisplays      func(uint32, *uint32, *uint32) int32
	gammaCapacity          func(uint32) uint32
	getGamma               func(uint32, uint32, *float32, *float32, *float32, *uint32) int32
	setGamma               func(uint32, uint32, *float32, *float32, *float32) int32
	displayOnline          func(uint32) bool
	restoreColorSync       func()
	applicationEventTarget func() uintptr
	installHandler         func(uintptr, uintptr, uint32, *eventType, uintptr, *uintptr) int32
	removeHandler          func(uintptr) int32
	registerHotKey         func(uint32, uint32, hotKeyID, uintptr, uint32, *uintptr) int32
	unregisterHotKey       func(uintptr) int32
	receiveEvent           func(uint32, *eventType, float64, bool, *uintptr) int32
	sendEvent              func(uintptr, uintptr) int32
	releaseEvent           func(uintptr)
	runLoopMode            func(uintptr, float64, bool) int32
	defaultRunLoopMode     uintptr
)

func loadPrivacy() error {
	if err := load(); err != nil {
		return err
	}
	privacyOnce.Do(func() {
		cg, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			privacyLoadErr = err
			return
		}
		cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			privacyLoadErr = err
			return
		}
		carbon, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			privacyLoadErr = err
			return
		}
		for _, f := range []struct {
			fn   any
			lib  uintptr
			name string
		}{
			{&getOnlineDisplays, cg, "CGGetOnlineDisplayList"}, {&gammaCapacity, cg, "CGDisplayGammaTableCapacity"},
			{&getGamma, cg, "CGGetDisplayTransferByTable"}, {&setGamma, cg, "CGSetDisplayTransferByTable"},
			{&displayOnline, cg, "CGDisplayIsOnline"}, {&restoreColorSync, cg, "CGDisplayRestoreColorSyncSettings"},
			{&applicationEventTarget, carbon, "GetApplicationEventTarget"}, {&installHandler, carbon, "InstallEventHandler"},
			{&removeHandler, carbon, "RemoveEventHandler"}, {&registerHotKey, carbon, "RegisterEventHotKey"},
			{&unregisterHotKey, carbon, "UnregisterEventHotKey"}, {&receiveEvent, carbon, "ReceiveNextEvent"},
			{&sendEvent, carbon, "SendEventToEventTarget"}, {&releaseEvent, carbon, "ReleaseEvent"},
			{&runLoopMode, cf, "CFRunLoopRunInMode"},
		} {
			p, e := purego.Dlsym(f.lib, f.name)
			if e != nil {
				privacyLoadErr = fmt.Errorf("desktopcontrol: missing %s: %w", f.name, ErrUnsupported)
				return
			}
			purego.RegisterFunc(f.fn, p)
		}
		mode, err := constID(cf, "kCFRunLoopDefaultMode")
		defaultRunLoopMode, privacyLoadErr = uintptr(mode), err
		if err == nil {
			privacyHandler = purego.NewCallback(func(_, _, _ uintptr) int32 { privacyExit.Store(true); return 0 })
		}
	})
	return privacyLoadErr
}

type gammaTable struct {
	id      uint32
	r, g, b []float32
}
type privacyState struct {
	tables          []gammaTable
	hotkey, handler uintptr
}

func (p *privacyState) close() {
	if p.hotkey != 0 {
		unregisterHotKey(p.hotkey)
	}
	if p.handler != 0 {
		removeHandler(p.handler)
	}
	fallback := false
	for _, t := range p.tables {
		if displayOnline(t.id) && setGamma(t.id, uint32(len(t.r)), &t.r[0], &t.g[0], &t.b[0]) != 0 {
			fallback = true
		}
	}
	if fallback {
		restoreColorSync()
	}
}

func blackGamma(id uint32) bool {
	var zero, r, g, b [256]float32
	if setGamma(id, 256, &zero[0], &zero[0], &zero[0]) != 0 {
		return false
	}
	var n uint32
	if getGamma(id, 256, &r[0], &g[0], &b[0], &n) != 0 || n == 0 || n > 256 {
		return false
	}
	for _, channel := range [][256]float32{r, g, b} {
		for _, v := range channel[:n] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || math.Abs(float64(v)) > 0.001 {
				return false
			}
		}
	}
	return true
}

func (p *privacyState) sync() bool {
	var ids [32]uint32
	var n uint32
	if getOnlineDisplays(32, &ids[0], &n) != 0 || n == 0 || n >= 32 {
		return false
	}
	for _, id := range ids[:n] {
		saved := false
		for _, t := range p.tables {
			if t.id == id {
				saved = true
				break
			}
		}
		if !saved {
			if len(p.tables) >= 32 {
				return false
			}
			cap := gammaCapacity(id)
			if cap == 0 || cap > 65536 {
				return false
			}
			t := gammaTable{id: id, r: make([]float32, cap), g: make([]float32, cap), b: make([]float32, cap)}
			var samples uint32
			if getGamma(id, cap, &t.r[0], &t.g[0], &t.b[0], &samples) != 0 || samples == 0 || samples > cap {
				return false
			}
			t.r, t.g, t.b = t.r[:samples], t.g[:samples], t.b[:samples]
			p.tables = append(p.tables, t)
		}
		if !blackGamma(id) {
			return false
		}
	}
	return true
}

func runPrivacy(ctx context.Context, ready func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !privacyActive.CompareAndSwap(false, true) {
		return errors.New("desktopcontrol: privacy already active")
	}
	defer privacyActive.Store(false)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := loadPrivacy(); err != nil {
		return err
	}
	withPool(func() {
		class("NSApplication").Send(sel("sharedApplication")).Send(sel("setActivationPolicy:"), nsApplicationActivationPolicyProhibited)
	})
	privacyExit.Store(false)
	var p privacyState
	defer p.close()
	spec := eventType{Class: 0x6b657962, Kind: 5}
	target := applicationEventTarget()
	if installHandler(target, privacyHandler, 1, &spec, 0, &p.handler) != 0 ||
		registerHotKey(35, (1<<8)|(1<<11)|(1<<12), hotKeyID{Signature: 0x4d795072, ID: 1}, target, 0, &p.hotkey) != 0 {
		return errors.New("desktopcontrol: privacy recovery shortcut unavailable")
	}
	if !p.sync() {
		return errors.New("desktopcontrol: displays do not support privacy mode")
	}
	if ready != nil {
		if err := ready(); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			runLoopMode(defaultRunLoopMode, 0.001, true)
			var event uintptr
			for receiveEvent(1, &spec, 0, true, &event) == 0 {
				sendEvent(event, target)
				releaseEvent(event)
			}
			if privacyExit.Load() {
				return nil
			}
			if !p.sync() {
				return errors.New("desktopcontrol: privacy display changed or unavailable")
			}
		}
	}
}
