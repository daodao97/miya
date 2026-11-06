package conversation

import (
	"regexp"
)

var mentionPlaceholder = regexp.MustCompile("\ue000([0-9]+)\ue001")
