package main

import (
	"regexp"
)

var mentionPlaceholder = regexp.MustCompile("\ue000([0-9]+)\ue001")

// Replace only explicit reference objects; Markdown keeps ownership of code,
// lists and formatting. Titles and IDs never serve as reverse lookup keys.
