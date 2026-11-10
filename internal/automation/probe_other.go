//go:build !unix

package automation

import "errors"

func diskFree(string) (uint64, error) {
	return 0, errors.New("free space is not available on this system")
}
