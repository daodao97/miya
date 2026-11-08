package codex

import "errors"

// Refused reports whether the app-server answered a request with an error,
// which proves it did not act on it. A timeout or a lost connection proves
// nothing: the server may already have taken the input.
func Refused(err error) bool {
	var rpc *RPCError
	return errors.As(err, &rpc)
}
