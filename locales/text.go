package locales

import (
	"encoding/json"
	"sync"
)

// sourceKeys lets existing English message templates use the same checked
// catalog as the generated accessors. Only app-owned copy calls TextIn;
// conversation content and names are never passed through it.
var sourceKeys = sync.OnceValue(func() map[string]string {
	data, err := files.ReadFile("en.json")
	if err != nil {
		panic(err)
	}
	var messages map[string]json.RawMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		panic(err)
	}
	keys := make(map[string]string, len(messages))
	for key := range messages {
		if len(key) > 0 && key[0] != '@' {
			keys[Catalog.Format("en", key, nil)] = key
		}
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
