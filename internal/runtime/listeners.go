package runtime

import (
	"net"
	"strconv"
	"strings"
)

// ParseListeners accepts lsof's NUL-delimited output. It keeps each concrete
// address separately and joins only identities already verified by the caller.
func ParseListeners(data []byte, owned map[int]Identity) []Endpoint {
	pid := 0
	family := ""
	out := []Endpoint{}
	seen := map[Endpoint]bool{}
	for _, field := range strings.Split(string(data), "\x00") {
		field = strings.TrimLeft(field, "\n")
		if len(field) < 2 {
			continue
		}
		switch field[0] {
		case 'p':
			pid, _ = strconv.Atoi(field[1:])
			family = ""
		case 'f':
			family = ""
		case 't':
			family = field[1:]
		case 'n':
			identity, ok := owned[pid]
			if !ok {
				continue
			}
			host, value, err := net.SplitHostPort(field[1:])
			if err != nil {
				continue
			}
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				continue
			}
			switch host {
			case "*":
				if family == "IPv6" {
					host = "::1"
				} else {
					host = "127.0.0.1"
				}
			case "0.0.0.0":
				host = "127.0.0.1"
			case "::":
				host = "::1"
			case "127.0.0.1", "::1":
			default:
				if !LocalHost(host) {
					continue
				}
			}
			endpoint := Endpoint{Process: identity, Host: host, Port: port, Protocol: "tcp"}
			if !seen[endpoint] {
				seen[endpoint] = true
				out = append(out, endpoint)
			}
		}
	}
	return out
}
