//go:build darwin && !ios

package desktopcontrol

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	nsCompositingOperationCopy = 1
	nsBitmapImageFileTypePNG   = 4
)

var (
	appsOnce sync.Once
	appsErr  error

	// Messages whose arguments objc.Send cannot pass: rectangles by value
	// and ten arguments.
	sendDrawInRect func(obj objc.ID, s objc.SEL, dst, src cgRect, op uint, fraction float64)
	sendInitBitmap func(obj objc.ID, s objc.SEL, planes unsafe.Pointer, w, h, bps, spp int, alpha, planar bool, space objc.ID, bpr, bpp int) objc.ID

	nsDeviceRGBColorSpace objc.ID
)

func loadApps() error {
	if err := load(); err != nil {
		return err
	}
	appsOnce.Do(func() {
		libobjc, err := purego.Dlopen(libObjCPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			appsErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		msgSend, err := purego.Dlsym(libobjc, "objc_msgSend")
		if err != nil {
			appsErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		purego.RegisterFunc(&sendDrawInRect, msgSend)
		purego.RegisterFunc(&sendInitBitmap, msgSend)
		appKit, err := purego.Dlopen(appKitPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			appsErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		nsDeviceRGBColorSpace, appsErr = constID(appKit, "NSDeviceRGBColorSpace")
	})
	return appsErr
}

var errNoBundle = errors.New("desktopcontrol: not an app bundle")

func applicationInfo(path string, iconSize int) (Application, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.HasSuffix(path, ".app") {
		return Application{}, errNoBundle
	}
	if err := loadApps(); err != nil {
		return Application{}, err
	}
	var app Application
	var err error
	withPool(func() {
		bundle := class("NSBundle").Send(sel("bundleWithPath:"), nsString(path))
		if bundle == 0 {
			err = errNoBundle
			return
		}
		stringClass := class("NSString")
		info := func(key string) string {
			v := bundle.Send(sel("objectForInfoDictionaryKey:"), nsString(key))
			if v == 0 || !objc.Send[bool](v, sel("isKindOfClass:"), stringClass) {
				return ""
			}
			return goString(v)
		}
		app.Name = info("CFBundleDisplayName")
		if app.Name == "" {
			app.Name = info("CFBundleName")
		}
		if app.Name == "" {
			app.Name = strings.TrimSuffix(filepath.Base(path), ".app")
		}
		app.BundleID = goString(bundle.Send(sel("bundleIdentifier")))
		if iconSize >= 1 && iconSize <= 512 {
			app.Icon = iconPNG(path, iconSize)
		}
	})
	return app, err
}

// iconPNG draws the file's icon into a size×size PNG. Call it in a pool.
func iconPNG(path string, size int) []byte {
	image := class("NSWorkspace").Send(sel("sharedWorkspace")).Send(sel("iconForFile:"), nsString(path))
	if image == 0 {
		return nil
	}
	rep := sendInitBitmap(class("NSBitmapImageRep").Send(sel("alloc")),
		sel("initWithBitmapDataPlanes:pixelsWide:pixelsHigh:bitsPerSample:samplesPerPixel:hasAlpha:isPlanar:colorSpaceName:bytesPerRow:bitsPerPixel:"),
		nil, size, size, 8, 4, true, false, nsDeviceRGBColorSpace, 0, 0)
	if rep == 0 {
		return nil
	}
	defer rep.Send(sel("release"))
	ctx := class("NSGraphicsContext").Send(sel("graphicsContextWithBitmapImageRep:"), rep)
	if ctx == 0 {
		return nil
	}
	gc := class("NSGraphicsContext")
	gc.Send(sel("saveGraphicsState"))
	gc.Send(sel("setCurrentContext:"), ctx)
	n := float64(size)
	sendDrawInRect(image, sel("drawInRect:fromRect:operation:fraction:"), cgRect{W: n, H: n}, cgRect{}, nsCompositingOperationCopy, 1)
	gc.Send(sel("restoreGraphicsState"))
	png := rep.Send(sel("representationUsingType:properties:"), uint(nsBitmapImageFileTypePNG), class("NSDictionary").Send(sel("dictionary")))
	if png == 0 {
		return nil
	}
	length := objc.Send[uint](png, sel("length"))
	bytes := objc.Send[unsafe.Pointer](png, sel("bytes"))
	if length == 0 || bytes == nil {
		return nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(bytes), length)...)
}
