package textutil

import (
	"bytes"
	"encoding/json"
)

// SameJSON compares a wire identity, ignoring insignificant whitespace.
func SameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return string(a) == string(b)
	}
	return x.String() == y.String()
}
