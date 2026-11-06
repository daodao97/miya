package msgfmt

import (
	"fmt"
	"strings"

	"miya/internal/agent/claude/native/transcript"
	"miya/internal/model"
	"miya/internal/textutil"
)

// changedFiles are the files a conversation's tools wrote, once each.
func ChangedFiles(messages []model.Message) []string {
	var files []string
	seen := map[string]bool{}
	add := func(path string) {
		if path = strings.TrimSpace(path); path != "" && !seen[path] {
			seen[path] = true
			files = append(files, path)
		}
	}
	for _, m := range messages {
		switch strings.ToLower(m.Kind) {
		case "filechange":
			if m.Item != nil {
				for _, change := range m.Item.Changes {
					add(change.Path)
				}
			}
		case "claudetool":
			switch name, input := transcript.ToolCall(m); name {
			case "Edit", "MultiEdit", "Write", "NotebookEdit":
				add(transcript.InputString(input, "file_path", "notebook_path"))
			}
		}
	}
	return files
}

// lastReply is the last thing an agent said in a turn.
func LastReply(turn []model.Message) string {
	for i := len(turn) - 1; i >= 0; i-- {
		if turn[i].Role == "assistant" && strings.TrimSpace(turn[i].Content) != "" {
			return strings.TrimSpace(turn[i].Content)
		}
	}
	return ""
}

func PacketSection(b *strings.Builder, title, body string) {
	fmt.Fprintf(b, "\n## %s\n%s\n", title, body)
}

func ThreadRefTask(text string) string {
	own, _ := model.SplitReferenceContext(text)
	var b strings.Builder
	end := 0
	for _, m := range model.ThreadMentions(own) {
		b.WriteString(own[end:m.Start])
		end = m.End
	}
	b.WriteString(own[end:])
	return textutil.ClipRunes(strings.Join(strings.Fields(textutil.FirstLine(strings.TrimSpace(b.String()))), " "), 80)
}

// threadRefs are stable backend identities in the message's own words.
func ThreadRefs(text string) []model.ThreadRef {
	own, _ := model.SplitReferenceContext(text)
	var refs []model.ThreadRef
	seen := map[model.ThreadRef]bool{}
	for _, mention := range model.ThreadMentions(own) {
		if !seen[mention.Ref] {
			seen[mention.Ref] = true
			refs = append(refs, mention.Ref)
		}
	}
	return refs
}
