//go:build darwin && !ios

package desktopcontrol

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	dialogDepth   = 8
	dialogBudget  = 160
	dialogTexts   = 20
	dialogButtons = 8
)

// skippedRoles are never read: editable text (secure fields are text
// fields), and navigation and document contents, which are not what a
// dialog says.
var skippedRoles = map[string]bool{
	"AXTextField": true, "AXTextArea": true, "AXComboBox": true,
	"AXScrollArea": true, "AXOutline": true, "AXTable": true,
	"AXToolbar": true, "AXMenuBar": true, "AXWebArea": true,
}

var (
	dialogOnce sync.Once
	dialogErr  error

	cfEqual func(a, b uintptr) bool
)

func loadDialog() error {
	if err := loadAX(); err != nil {
		return err
	}
	dialogOnce.Do(func() {
		cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			dialogErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		p, err := purego.Dlsym(cf, "CFEqual")
		if err != nil {
			dialogErr = fmt.Errorf("desktopcontrol: missing CFEqual: %w", ErrUnsupported)
			return
		}
		purego.RegisterFunc(&cfEqual, p)
	})
	return dialogErr
}

func isKind(o objc.ID, name string) bool {
	return o != 0 && objc.Send[bool](o, sel("isKindOfClass:"), class(name))
}

// axString reads a string attribute of elem. Call it in a pool.
func axString(elem uintptr, attr string) string {
	v := axCopy(elem, attr)
	if v == 0 {
		return ""
	}
	defer cfRelease(v)
	if !isKind(objc.ID(v), "NSString") {
		return ""
	}
	return goString(objc.ID(v))
}

func axEnabled(elem uintptr) bool {
	v := axCopy(elem, "AXEnabled")
	if v == 0 {
		return false
	}
	defer cfRelease(v)
	return isKind(objc.ID(v), "NSNumber") && objc.Send[bool](objc.ID(v), sel("boolValue"))
}

// axButton is a button found in a dialog; elem is retained.
type axButton struct {
	title string
	elem  uintptr
}

func releaseButtons(buttons []axButton) {
	for _, b := range buttons {
		cfRelease(b.elem)
	}
}

// walk collects static labels and buttons under elem, depth first.
func walk(elem uintptr, texts *[]string, buttons *[]axButton, depth int, budget *int) {
	if depth > dialogDepth {
		return
	}
	if *budget--; *budget < 0 {
		return
	}
	axUIElementSetMessagingTimeout(elem, 0.1)
	role := axString(elem, "AXRole")
	if skippedRoles[role] {
		return
	}
	switch role {
	case "AXStaticText":
		text := axString(elem, "AXValue")
		if text != "" && len(*texts) < dialogTexts && utf8.RuneCountInString(text) <= 2000 && !slices.Contains(*texts, text) {
			*texts = append(*texts, text)
		}
	case "AXButton":
		title := axString(elem, "AXTitle")
		if title != "" && utf8.RuneCountInString(title) <= 200 && len(*buttons) < dialogButtons {
			*buttons = append(*buttons, axButton{title, cfRetain(elem)})
		}
	}
	children := axCopy(elem, "AXChildren")
	if children == 0 {
		return
	}
	defer cfRelease(children)
	arr := objc.ID(children)
	if !isKind(arr, "NSArray") {
		return
	}
	for i := range objc.Send[uint](arr, sel("count")) {
		if *budget <= 0 {
			break
		}
		walk(uintptr(arr.Send(sel("objectAtIndex:"), i)), texts, buttons, depth+1, budget)
	}
}

// dialogOf finds w's AX window and collects its labels and buttons. Call
// it in a pool; the caller releases window and the buttons.
func dialogOf(w Window) (window uintptr, texts []string, buttons []axButton, err error) {
	bounds, ok := currentBounds(w)
	if !ok {
		return 0, nil, nil, ErrWindowUnavailable
	}
	if window = copyWindowElement(bounds, w.PID); window == 0 {
		return 0, nil, nil, ErrWindowUnavailable
	}
	budget := dialogBudget
	walk(window, &texts, &buttons, 0, &budget)
	return window, texts, buttons, nil
}

func dialogReady(w Window) error {
	if w.ID == 0 || w.PID <= 0 {
		return ErrWindowUnavailable
	}
	if err := loadDialog(); err != nil {
		return err
	}
	if !axIsProcessTrusted() {
		return ErrAccessibility
	}
	return nil
}

func readDialog(w Window) (Dialog, error) {
	if err := dialogReady(w); err != nil {
		return Dialog{}, err
	}
	var d Dialog
	var err error
	withPool(func() {
		var window uintptr
		var texts []string
		var buttons []axButton
		if window, texts, buttons, err = dialogOf(w); err != nil {
			return
		}
		defer cfRelease(window)
		defer releaseButtons(buttons)
		def, cancel := axCopy(window, "AXDefaultButton"), axCopy(window, "AXCancelButton")
		if def != 0 {
			defer cfRelease(def)
		}
		if cancel != 0 {
			defer cfRelease(cancel)
		}
		for _, b := range buttons {
			a := DialogAction{
				Label:   b.title,
				Enabled: axEnabled(b.elem),
				Default: def != 0 && cfEqual(def, b.elem),
				Cancel:  cancel != 0 && cfEqual(cancel, b.elem),
			}
			if f, ok := axFrame(b.elem); ok {
				a.Bounds = f
			}
			d.Actions = append(d.Actions, a)
		}
		if len(texts) > 0 {
			d.Title = texts[0]
		} else {
			d.Title = axString(window, "AXTitle")
		}
		if len(texts) > 1 {
			d.Message = strings.Join(texts[1:], "\n")
		}
		if title, rest, ok := strings.Cut(d.Title, "\n"); ok {
			d.Title = title
			if d.Message != "" {
				d.Message = rest + "\n" + d.Message
			} else {
				d.Message = rest
			}
		}
	})
	return d, err
}

func press(w Window, label string) error {
	if label == "" || len(label) > 512 {
		return ErrWindowUnavailable
	}
	if err := dialogReady(w); err != nil {
		return err
	}
	var err error
	withPool(func() {
		var window uintptr
		var buttons []axButton
		if window, _, buttons, err = dialogOf(w); err != nil {
			return
		}
		defer cfRelease(window)
		defer releaseButtons(buttons)
		var match uintptr
		count := 0
		for _, b := range buttons {
			if b.title == label {
				match, count = b.elem, count+1
			}
		}
		if count != 1 || !axEnabled(match) || axUIElementPerformAction(match, uintptr(nsString("AXPress"))) != 0 {
			err = ErrWindowUnavailable
		}
	})
	return err
}
