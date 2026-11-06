package conversation

import (
	regexp "regexp"
)

var mentionPlaceholder = regexp.MustCompile("\ue000([0-9]+)\ue001")
