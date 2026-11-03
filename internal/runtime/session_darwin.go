package runtime

import (
	"bytes"
	"encoding/binary"
	"golang.org/x/sys/unix"
)

func CodexProcessSession(pid int) string {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return ""
	}
	return codexSessionProcargs(data)
}

func codexSessionProcargs(data []byte) string {
	if len(data) < 4 {
		return ""
	}
	argc := int(binary.NativeEndian.Uint32(data[:4]))
	data = data[4:]
	// The executable path is followed by NUL padding, then argc arguments.
	end := bytes.IndexByte(data, 0)
	if end < 0 {
		return ""
	}
	data = bytes.TrimLeft(data[end:], "\x00")
	for range argc {
		end = bytes.IndexByte(data, 0)
		if end < 0 {
			return ""
		}
		data = data[end+1:]
	}
	return codexSessionEnvironment(data)
}
