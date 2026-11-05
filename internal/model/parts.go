package model

import "sort"

type Mention struct {
	Start, End int
	Part       MessagePart
}

func Mentions(text string) []Mention {
	var out []Mention
	for _, m := range ThreadMentions(text) {
		ref := m.Ref
		out = append(out, Mention{m.Start, m.End, MessagePart{Type: "thread_ref", Ref: &ref, TitleSnapshot: m.Title}})
	}
	for _, m := range AgentMentions(text) {
		out = append(out, Mention{m.Start, m.End, MessagePart{Type: "agent_ref", Agent: m.Agent, TitleSnapshot: m.Title}})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start == out[j].Start {
			return out[i].End > out[j].End
		}
		return out[i].Start < out[j].Start
	})
	var merged []Mention
	end := 0
	for _, m := range out {
		if m.Start < end {
			continue
		}
		merged = append(merged, m)
		end = m.End
	}
	return merged
}

func MessageParts(text string) []MessagePart {
	var out []MessagePart
	end := 0
	for _, m := range Mentions(text) {
		if m.Start > end {
			out = append(out, MessagePart{Type: "text", Text: text[end:m.Start]})
		}
		out = append(out, m.Part)
		end = m.End
	}
	if end < len(text) {
		out = append(out, MessagePart{Type: "text", Text: text[end:]})
	}
	return out
}
