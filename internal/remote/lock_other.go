//go:build !unix

package remote

import "os"

// Without flock, a second Miya instance may share the identity; the relay
// then serves whichever connected last.
func tryLock(*os.File) (bool, error) { return true, nil }
