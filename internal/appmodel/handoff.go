package appmodel

import (
	model "miya/internal/model"
	textutil "miya/internal/textutil"
	strings "strings"
)

func ThreadRefTitle(t Thread) string {
	return textutil.FirstNonEmpty(strings.TrimSpace(t.Title), t.ServerID, tr("New conversation"))
}

// threadContextDisplay shows the context packets of a message as the
// conversations they name, not the text the agent read.
func ThreadContextDisplay(s string) string {
	own, _ := model.SplitReferenceContext(s)
	var b strings.Builder
	end := 0
	for _, m := range model.ThreadMentions(own) {
		b.WriteString(own[end:m.Start])
		b.WriteString(tr("[Referenced conversation: ") + m.Title + "]")
		end = m.End
	}
	b.WriteString(own[end:])
	return b.String()
}
