//go:build darwin && !ios

package desktopcontrol

import (
	"context"
	"fmt"
	"math"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var (
	captureOnce              sync.Once
	captureLoadErr           error
	nsImageCompressionFactor objc.ID
)

func loadCapture() error {
	if !available() {
		return ErrUnsupported
	}
	captureOnce.Do(func() {
		if _, err := purego.Dlopen("/System/Library/Frameworks/ScreenCaptureKit.framework/ScreenCaptureKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
			captureLoadErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		appkit, err := purego.Dlopen(appKitPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			captureLoadErr = err
			return
		}
		nsImageCompressionFactor, captureLoadErr = constID(appkit, "NSImageCompressionFactor")
	})
	return captureLoadErr
}

// nativeFailure keeps domain/code only: localized descriptions can include
// document names. The NSError and every borrowed image stay in their callback.
func nativeFailure(kind error, e objc.ID) error {
	if e == 0 {
		return kind
	}
	return fmt.Errorf("%w (%s (%d))", kind, goString(e.Send(sel("domain"))), objc.Send[int](e, sel("code")))
}

func jpegImage(image objc.ID, quality float64) ([]byte, error) {
	rep := class("NSBitmapImageRep").Send(sel("alloc")).Send(sel("initWithCGImage:"), image)
	if rep == 0 {
		return nil, ErrEncoding
	}
	defer rep.Send(sel("release"))
	factor := class("NSNumber").Send(sel("numberWithDouble:"), quality)
	props := class("NSDictionary").Send(sel("dictionaryWithObject:forKey:"), factor, nsImageCompressionFactor)
	data := rep.Send(sel("representationUsingType:properties:"), uint(3), props) // NSBitmapImageFileTypeJPEG
	if data == 0 {
		return nil, ErrEncoding
	}
	n := objc.Send[uint](data, sel("length"))
	p := objc.Send[unsafe.Pointer](data, sel("bytes"))
	if n == 0 || p == nil {
		return nil, ErrEncoding
	}
	return append([]byte(nil), unsafe.Slice((*byte)(p), n)...), nil
}

type captureResult struct {
	data []byte
	err  error
}

func capture(ctx context.Context, d Display, w Window, o CaptureOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := loadCapture(); err != nil {
		return nil, err
	}
	if !cgPreflightScreenCaptureAccess() {
		return nil, ErrScreenRecording
	}
	result := make(chan captureResult, 1)
	finish := func(data []byte, err error) {
		select {
		case result <- captureResult{data, err}:
		default:
		}
	}
	withPool(func() {
		// purego v0.11 caches native trampolines by block signature. Each call
		// owns only its closure, released when the framework releases its block.
		contentBlock := objc.NewBlock(func(_ objc.Block, content, e objc.ID) {
			withPool(func() {
				if ctx.Err() != nil {
					return
				}
				if e != 0 || content == 0 {
					finish(nil, nativeFailure(ErrShareableContent, e))
					return
				}
				var filter objc.ID
				var width, height float64
				if w.ID != 0 {
					list := content.Send(sel("windows"))
					for i := range objc.Send[uint](list, sel("count")) {
						window := list.Send(sel("objectAtIndex:"), i)
						if objc.Send[uint32](window, sel("windowID")) != w.ID || int(objc.Send[int32](window.Send(sel("owningApplication")), sel("processID"))) != w.PID {
							continue
						}
						filter = class("SCContentFilter").Send(sel("alloc")).Send(sel("initWithDesktopIndependentWindow:"), window)
						frame := objc.Send[cgRect](window, sel("frame"))
						width, height = frame.W, frame.H
						break
					}
				} else {
					if !displayMatches(d.ID, d.UUID) {
						finish(nil, ErrDisplayUnavailable)
						return
					}
					list := content.Send(sel("displays"))
					for i := range objc.Send[uint](list, sel("count")) {
						display := list.Send(sel("objectAtIndex:"), i)
						if objc.Send[uint32](display, sel("displayID")) != d.ID {
							continue
						}
						filter = class("SCContentFilter").Send(sel("alloc")).Send(sel("initWithDisplay:excludingWindows:"), display, class("NSArray").Send(sel("array")))
						width, height = float64(objc.Send[int](display, sel("width"))), float64(objc.Send[int](display, sel("height")))
						break
					}
				}
				if filter != 0 {
					defer filter.Send(sel("release"))
				}
				if filter == 0 || width <= 0 || height <= 0 {
					if w.ID != 0 {
						finish(nil, ErrWindowUnavailable)
					} else {
						finish(nil, ErrDisplayUnavailable)
					}
					return
				}
				scale := 1.0
				if o.MaxDimension > 0 {
					scale = math.Min(1, float64(o.MaxDimension)/math.Max(width, height))
				}
				config := class("SCStreamConfiguration").Send(sel("alloc")).Send(sel("init"))
				defer config.Send(sel("release"))
				config.Send(sel("setWidth:"), uint(math.Max(1, width*scale)))
				config.Send(sel("setHeight:"), uint(math.Max(1, height*scale)))
				config.Send(sel("setShowsCursor:"), w.ID == 0)
				config.Send(sel("setIgnoreShadowsSingleWindow:"), true)
				config.Send(sel("setIgnoreGlobalClipSingleWindow:"), true)
				shotBlock := objc.NewBlock(func(_ objc.Block, image, err objc.ID) {
					withPool(func() {
						if ctx.Err() != nil {
							return
						}
						if err != 0 || image == 0 {
							finish(nil, nativeFailure(ErrCapture, err))
							return
						}
						data, failure := jpegImage(image, o.Quality)
						if ctx.Err() == nil {
							finish(data, failure)
						}
					})
				})
				defer shotBlock.Release()
				class("SCScreenshotManager").Send(sel("captureImageWithFilter:configuration:completionHandler:"), filter, config, shotBlock)
			})
		})
		defer contentBlock.Release()
		class("SCShareableContent").Send(sel("getShareableContentExcludingDesktopWindows:onScreenWindowsOnly:completionHandler:"), false, true, contentBlock)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-result:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return r.data, r.err
	}
}
