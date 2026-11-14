package locales

import "sync"

// sourceKeys lets existing English message templates use the same checked
// catalog as the generated accessors. Only app-owned copy calls TextIn;
// conversation content and names are never passed through it.
var sourceKeys = sync.OnceValue(func() map[string]string {
	messages := Catalog.Keys()
	keys := make(map[string]string, len(messages))
	for _, key := range messages {
		keys[Catalog.Format("en", key, nil)] = key
	}
	return keys
})

// TextIn translates a source message, preserving unknown text verbatim.
func TextIn(locale, source string) string {
	if key, ok := sourceKeys()[source]; ok {
		return Catalog.Format(locale, key, nil)
	}
	return source
}

// HasMessage reports whether a source message is in the app's catalog.
func HasMessage(source string) bool {
	_, ok := sourceKeys()[source]
	return ok
}
