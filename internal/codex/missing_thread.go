package codex

import (
	"errors"
	"strings"
)

func IsMissingThread(err error, threadID string) bool {
	var rpc *RPCError
	return errors.As(err, &rpc) && rpc.Code == -32600 &&
		strings.TrimSpace(rpc.Message) == "thread not found: "+threadID
}
