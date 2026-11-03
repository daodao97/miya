package runtime

import "bytes"

// Extract only the execution session marker. Other environment values never
// leave this parser and must not be logged or persisted.
func codexSessionEnvironment(data []byte) string {
	const key = "CODEX_THREAD_ID="
	for _, field := range bytes.Split(data, []byte{0}) {
		if bytes.HasPrefix(field, []byte(key)) {
			return string(field[len(key):])
		}
	}
	return ""
}
