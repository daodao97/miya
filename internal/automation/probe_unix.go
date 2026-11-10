//go:build unix

package automation

import "syscall"

// diskFree is the space available to Miya's user on the volume holding
// path, in bytes. On macOS it leaves out purgeable space, which Finder
// counts as available.
func diskFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
