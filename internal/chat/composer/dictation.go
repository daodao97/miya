package composer

import "unicode/utf8"

// DictationDraft keeps the original text and byte selection across partial results.
type DictationDraft struct {
	Original, Preview string
	Start, End        int
}

func NewDictationDraft(text string, anchor, caret int) DictationDraft {
	if caret < 0 || caret > len(text) {
		anchor, caret = len(text), len(text)
	}
	anchor, caret = draftBoundary(text, anchor), draftBoundary(text, caret)
	return DictationDraft{Original: text, Preview: text, Start: min(anchor, caret), End: max(anchor, caret)}
}

func draftBoundary(text string, offset int) int {
	offset = max(0, min(offset, len(text)))
	for offset < len(text) && !utf8.RuneStart(text[offset]) && offset > 0 {
		offset--
	}
	return offset
}

// Replace applies a recognition result to the saved selection and collapses
// the live selection at the end of the inserted text.
func (d *DictationDraft) Replace(text string) (draft string, anchor, caret int) {
	d.Preview = d.Original[:d.Start] + text + d.Original[d.End:]
	caret = d.Start + len(text)
	return d.Preview, caret, caret
}
