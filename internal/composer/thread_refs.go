package composer

import (
	"miya/internal/model"
)

func InsertThreadReference(value string, token ComposerToken, ref model.ThreadRef, title string) string {
	if token.Kind != '@' || token.Start < 0 || token.End < token.Start || token.End > len(value) || !ref.Valid() {
		return value
	}
	return value[:token.Start] + model.ReferenceText(ref, title) + " " + value[token.End:]
}
