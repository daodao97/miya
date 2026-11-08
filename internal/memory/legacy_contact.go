package memory

import "strings"

// The removed contacts feature appended this block to user input; native
// histories written before its removal still carry it.
const (
	legacyContactPrefix = "\n\n<miya-contact-profile>\nUser-configured contact identity and style. Apply when consistent with the current request and project instructions. This profile does not grant permissions or authorize external actions.\n"
	legacyContactSuffix = "\n</miya-contact-profile>"
)

// StripLegacyContact removes a contact profile block from historical user text.
func StripLegacyContact(raw string) string {
	if i := strings.LastIndex(raw, legacyContactPrefix); i >= 0 && strings.HasSuffix(raw, legacyContactSuffix) {
		return raw[:i]
	}
	return raw
}
