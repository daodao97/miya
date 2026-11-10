package mobile

import "testing"

func TestNotificationIDCarriesItsTarget(t *testing.T) {
	data := map[string]string{"thread": "4", "serverId": "a#b&c", "desktop": "d"}
	key, got, ok := parseNotificationID(notificationID("n-1", data))
	if !ok || key != "n-1" || len(got) != len(data) {
		t.Fatalf("%q %v %v", key, got, ok)
	}
	for k, v := range data {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if _, _, ok := parseNotificationID("8F2C-APNS-UUID"); ok {
		t.Error("a push's ID was taken for Miya's")
	}
}
