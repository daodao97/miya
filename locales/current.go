package locales

import "sync/atomic"

// current is the interface language the UI last matched. It belongs to this
// device; code outside a ui.Context reads it through T.
var current atomic.Value

// SetCurrent records the matched interface language tag.
func SetCurrent(tag string) { current.Store(tag) }

// Current returns the matched interface language tag, or "" before the UI
// has matched one.
func Current() string {
	tag, _ := current.Load().(string)
	return tag
}

// T translates an English source message into the current language.
//
// Packages call it through a package-private
//
//	func tr(s string) string { return locales.T(s) }
//
// so TestProductSourceCopyUsesEnglishCatalog finds every tr("…") literal and
// checks it against the catalog.
func T(source string) string { return TextIn(Current(), source) }

// Match returns the catalog's language that suits the preferred tags best,
// the source language when none does.
func Match(preferred []string) string {
	return Catalog.Match(preferred)
}

var messageLocaleFunc atomic.Pointer[func() string]

// SetMessageLocaleFunc lets the legacy UI supply its application locale to the
// generated accessors. With nil, accessors use Current, as the NSGo UI does.
// The function must be safe to call concurrently.
func SetMessageLocaleFunc(f func() string) {
	if f == nil {
		messageLocaleFunc.Store(nil)
	} else {
		messageLocaleFunc.Store(&f)
	}
}

func messageLocale() string {
	if f := messageLocaleFunc.Load(); f != nil {
		return (*f)()
	}
	return Current()
}
