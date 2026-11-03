package runtime

import (
	"os"
	"strconv"
)

func CodexProcessSession(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return ""
	}
	return codexSessionEnvironment(data)
}
