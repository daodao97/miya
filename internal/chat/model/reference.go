package model

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"miya/internal/markdown/parser"
)

func ParseThreadURL(raw string) (ThreadRef, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "miya" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ThreadRef{}, false
	}
	p := strings.Split(u.EscapedPath(), "/")
	if len(p) != 4 || p[0] != "" || p[1] != "thread" {
		return ThreadRef{}, false
	}
	sid, err := url.PathUnescape(p[3])
	r := ThreadRef{Source: u.Host, Backend: p[2], SessionID: sid}
	return r, err == nil && r.Valid() && r.URL() == raw
}

var threadMentionPattern = regexp.MustCompile(`@\[((?:\\.|[^\]\\\r\n])*)\]\((miya://[^\s<>()\[\]]+)\)`)

type ThreadMention struct {
	Start, End int
	Ref        ThreadRef
	Title      string
}

func ReferenceText(ref ThreadRef, title string) string {
	if ref.URL() == "" {
		return ""
	}
	title = strings.Join(strings.Fields(title), " ")
	title = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(title)
	return "@[" + title + "](" + ref.URL() + ")"
}

// Mask code using the same Markdown parser as the message renderer. Replace
// reference data with same-length neutral text first: a backtick in a title
// snapshot must not change the Markdown state of the surrounding sentence.
func ReferenceCodeMask(s string) []bool {
	mask := make([]bool, len(s))
	sanitized := []byte(s)
	for _, m := range threadMentionPattern.FindAllStringSubmatchIndex(s, -1) {
		if _, ok := ParseThreadURL(s[m[4]:m[5]]); !ok {
			continue
		}
		for i := m[0]; i < m[1]; i++ {
			sanitized[i] = 'x'
		}
	}
	for _, m := range agentMentionPattern.FindAllStringSubmatchIndex(s, -1) {
		if _, ok := ParseAgentURL(s[m[4]:m[5]]); !ok {
			continue
		}
		for i := m[0]; i < m[1]; i++ {
			sanitized[i] = 'x'
		}
	}
	doc := parser.Parse(string(sanitized))
	mark := func(start, end int) {
		for i := max(0, start); i < min(len(mask), end); i++ {
			mask[i] = true
		}
	}
	for i := 0; i < doc.Len(); i++ {
		block := doc.Block(i)
		if block.Kind == parser.CodeBlock || block.Kind == parser.HTMLBlock || block.Kind == parser.MathBlock {
			mark(block.Start, block.End)
			continue
		}
		for _, inline := range doc.Inlines(i) {
			if inline.Kind == parser.Code || inline.Kind == parser.HTML || inline.Kind == parser.Math {
				mark(inline.Start, inline.End)
			}
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			mask[i] = true
			if i+1 < len(s) {
				i++
				mask[i] = true
			}
		}
	}
	return mask
}

func ThreadMentions(text string) []ThreadMention {
	matches := threadMentionPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	mask := ReferenceCodeMask(text)
	var out []ThreadMention
	for _, m := range matches {
		if mask[m[0]] {
			continue
		}
		ref, ok := ParseThreadURL(text[m[4]:m[5]])
		if !ok {
			continue
		}
		title := strings.NewReplacer(`\\`, `\`, `\[`, `[`, `\]`, `]`).Replace(text[m[2]:m[3]])
		out = append(out, ThreadMention{Start: m[0], End: m[1], Ref: ref, Title: title})
	}
	return out
}

const ContextEnvelopeOpen = "\n\n<miya-thread-context version=\"2\">\n"
const ContextEnvelopeClose = "\n</miya-thread-context>"

type ReferenceSnapshot struct {
	Ref     ThreadRef `json:"ref"`
	Title   string    `json:"titleSnapshot"`
	Context string    `json:"context"`
}

func SplitReferenceContext(text string) (string, []ReferenceSnapshot) {
	start := strings.LastIndex(text, ContextEnvelopeOpen)
	if start < 0 {
		return text, nil
	}
	contentStart := start + len(ContextEnvelopeOpen)
	closeAt := strings.Index(text[contentStart:], ContextEnvelopeClose)
	if closeAt < 0 || ReferenceCodeMask(text[:start] + "\n\nsnapshot")[start+2] {
		return text, nil
	}
	contentEnd := contentStart + closeAt
	end := contentEnd + len(ContextEnvelopeClose)
	own := text[:start] + text[end:]
	var snapshots []ReferenceSnapshot
	data := text[contentStart:contentEnd]
	if json.Unmarshal([]byte(data), &snapshots) != nil || len(snapshots) == 0 {
		return text, nil
	}
	mentions := ThreadMentions(own)
	covered := map[ThreadRef]bool{}
	for _, s := range snapshots {
		found := false
		for _, m := range mentions {
			if m.Ref == s.Ref && m.Title == s.Title {
				found = true
				break
			}
		}
		if !s.Ref.Valid() || !found {
			return text, nil
		}
		covered[s.Ref] = true
	}
	for _, m := range mentions {
		if !covered[m.Ref] {
			return text, nil
		}
	}
	return own, snapshots
}

func JoinReferenceMessages(texts []string) string {
	var bodies []string
	var snapshots []ReferenceSnapshot
	for _, text := range texts {
		own, refs := SplitReferenceContext(text)
		bodies = append(bodies, own)
		snapshots = append(snapshots, refs...)
	}
	out := strings.Join(bodies, "\n\n")
	if len(snapshots) > 0 {
		data, _ := json.MarshalIndent(snapshots, "", "  ")
		out += ContextEnvelopeOpen + string(data) + ContextEnvelopeClose
	}
	return out
}

var agentMentionPattern = regexp.MustCompile(`#\[((?:\\.|[^\]\\\r\n])*)\]\((miya-agent://[^\s<>()\[\]]+)\)`)

func AgentURL(agent string) string {
	if !RegisteredBackend(agent) {
		return ""
	}
	return "miya-agent://" + agent
}

func ParseAgentURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "miya-agent" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || AgentURL(u.Host) != raw {
		return "", false
	}
	return u.Host, true
}

type AgentMention struct {
	Start, End   int
	Agent, Title string
}

func AgentMentions(text string) []AgentMention {
	indices := agentMentionPattern.FindAllStringSubmatchIndex(text, -1)
	if len(indices) == 0 {
		return nil
	}
	mask := ReferenceCodeMask(text)
	var out []AgentMention
	for _, m := range indices {
		if mask[m[0]] {
			continue
		}
		agent, ok := ParseAgentURL(text[m[4]:m[5]])
		if !ok {
			continue
		}
		title := strings.NewReplacer(`\\`, `\`, `\[`, `[`, `\]`, `]`).Replace(text[m[2]:m[3]])
		out = append(out, AgentMention{m[0], m[1], agent, title})
	}
	return out
}

func HasAgentReferences(text string, parts []MessagePart) bool {
	for _, part := range parts {
		if part.Type == "agent_ref" {
			return true
		}
	}
	own, _ := SplitReferenceContext(text)
	return len(AgentMentions(own)) > 0
}
