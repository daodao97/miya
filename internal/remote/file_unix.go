//go:build unix

package remote

import "syscall"

// A FIFO replacing the file between stat and open must not block the request.
const remoteFileOpenFlags = syscall.O_NONBLOCK
