package notification

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"miya/internal/remote"
)

// Data decodes string routing values from a push payload.
func Data(data []byte) map[string]string {
	var d map[string]string
	_ = json.Unmarshal(data, &d)
	return d
}

// ID encodes routing values into a local notification ID so a tap can open
// its conversation even after process restart.
func ID(key string, data map[string]string) string {
	q := url.Values{}
	for k, v := range data {
		q.Set(k, v)
	}
	return key + "#" + q.Encode()
}

// ParseID decodes a local notification ID; other identifiers return false.
func ParseID(id string) (key string, data map[string]string, ok bool) {
	key, query, ok := strings.Cut(id, "#")
	if !ok {
		return "", nil, false
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", nil, false
	}
	data = map[string]string{}
	for k := range q {
		data[k] = q.Get(k)
	}
	return key, data, true
}

func Target(data map[string]string) remote.Request {
	id, _ := strconv.Atoi(data["thread"])
	return remote.Request{Thread: id, ServerID: data["serverId"], Agent: data["agent"], Project: data["project"]}
}
