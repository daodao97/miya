package mobile

import "testing"

// A push carries Miya's data under "mygo" (apns.Payload): a tap tells the
// userInfo with an object there, SetPresentation with its JSON as a string.
func TestNotificationDataReadsThePushPayload(t *testing.T) {
	for name, data := range map[string]string{
		"tap":          `{"aps":{"alert":{"title":"Done"},"badge":2},"mygoID":"n-1","mygo":{"thread":"4","serverId":"s4","desktop":"d1"}}`,
		"presentation": `{"aps":"{}","mygoID":"n-1","mygo":"{\"thread\":\"4\",\"serverId\":\"s4\",\"desktop\":\"d1\"}"}`,
		"local":        `{"thread":"4","serverId":"s4","desktop":"d1"}`,
	} {
		got := notificationData([]byte(data))
		if got["thread"] != "4" || got["serverId"] != "s4" || got["desktop"] != "d1" {
			t.Errorf("%s: %v", name, got)
		}
		if target := notificationTarget(got); target.Thread != 4 || target.ServerID != "s4" {
			t.Errorf("%s: target %+v", name, target)
		}
	}
}
