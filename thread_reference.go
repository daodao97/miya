package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui/markdown/parser"
)

// ThreadRef identifies a backend session, never a window or a sidebar row.
type ThreadRef struct {
	Source    string `json:"source"`
	Backend   string `json:"backend"`
	SessionID string `json:"sessionId"`
}

func (r ThreadRef) valid() bool {
	return sourceIDPattern.MatchString(r.Source) && registeredBackend(r.Backend) && r.SessionID != "" && !strings.ContainsAny(r.SessionID, "\x00\r\n")
}

func (r ThreadRef) URL() string {
	if !r.valid() {
		return ""
	}
	return "miya://" + r.Source + "/thread/" + r.Backend + "/" + url.PathEscape(r.SessionID)
}

func parseThreadURL(raw string) (ThreadRef, bool) {
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
	return r, err == nil && r.valid() && r.URL() == raw
}

var sourceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var threadMentionPattern = regexp.MustCompile(`@\[((?:\\.|[^\]\\\r\n])*)\]\((miya://[^\s<>()\[\]]+)\)`)

// MessagePart is the lossless composer representation. The explicit text
// spelling @[title](URL) survives backend history and plain-text clipboard
// round trips; a bare URL is ordinary text and does not load any context.
type MessagePart struct {
	Type          string     `json:"type"`
	Text          string     `json:"text,omitempty"`
	Ref           *ThreadRef `json:"ref,omitempty"`
	TitleSnapshot string     `json:"titleSnapshot,omitempty"`
	Agent         string     `json:"agent,omitempty"`
}

type threadMention struct {
	Start, End int
	Ref        ThreadRef
	Title      string
}

func referenceText(ref ThreadRef, title string) string {
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
func referenceCodeMask(s string) []bool {
	mask := make([]bool, len(s))
	sanitized := []byte(s)
	for _, m := range threadMentionPattern.FindAllStringSubmatchIndex(s, -1) {
		if _, ok := parseThreadURL(s[m[4]:m[5]]); !ok {
			continue
		}
		for i := m[0]; i < m[1]; i++ {
			sanitized[i] = 'x'
		}
	}
	for _, m := range agentMentionPattern.FindAllStringSubmatchIndex(s, -1) {
		if _, ok := parseAgentURL(s[m[4]:m[5]]); !ok {
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

func threadMentions(text string) []threadMention {
	matches := threadMentionPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	mask := referenceCodeMask(text)
	var out []threadMention
	for _, m := range matches {
		if mask[m[0]] {
			continue
		}
		ref, ok := parseThreadURL(text[m[4]:m[5]])
		if !ok {
			continue
		}
		title := strings.NewReplacer(`\\`, `\`, `\[`, `[`, `\]`, `]`).Replace(text[m[2]:m[3]])
		out = append(out, threadMention{Start: m[0], End: m[1], Ref: ref, Title: title})
	}
	return out
}

func messageParts(text string) []MessagePart {
	var out []MessagePart
	end := 0
	for _, m := range composerMentions(text) {
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

func partsText(parts []MessagePart) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "text":
			b.WriteString(p.Text)
		case "thread_ref":
			if p.Ref == nil || !p.Ref.valid() {
				return "", errors.New("Invalid conversation reference identity")
			}
			b.WriteString(referenceText(*p.Ref, p.TitleSnapshot))
		case "agent_ref":
			tag := agentReferenceText(p.Agent, p.TitleSnapshot)
			if tag == "" {
				return "", errors.New("Invalid agent reference identity")
			}
			b.WriteString(tag)
		default:
			return "", fmt.Errorf("Unknown message content type %q", p.Type)
		}
	}
	return b.String(), nil
}

// loadReferenceSource persists a public data-instance ID, independent of pairing.
// Link the complete temporary file so concurrent initializers cannot replace it.
func loadReferenceSource(dir string) (string, error) {
	path := filepath.Join(dir, "reference-source")
	read := func() (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		id := strings.TrimSpace(string(data))
		if !sourceIDPattern.MatchString(id) {
			return "", errors.New("Invalid conversation reference source file")
		}
		return id, nil
	}
	if id, err := read(); !os.IsNotExist(err) {
		return id, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(bytes[:])
	f, err := os.CreateTemp(dir, ".reference-source-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(id + "\n"); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return "", err
	}
	return read()
}

func referenceSourceForService() (string, string) {
	id, err := loadReferenceSource(serviceDir())
	if err != nil {
		return "", "Unable to initialize conversation reference source: " + err.Error()
	}
	return id, ""
}

const contextEnvelopeOpen = "\n\n<miya-thread-context version=\"2\">\n"
const contextEnvelopeClose = "\n</miya-thread-context>"

type referenceSnapshot struct {
	Ref     ThreadRef `json:"ref"`
	Title   string    `json:"titleSnapshot"`
	Context string    `json:"context"`
}

func splitReferenceContext(text string) (string, []referenceSnapshot) {
	start := strings.LastIndex(text, contextEnvelopeOpen)
	if start < 0 {
		return text, nil
	}
	contentStart := start + len(contextEnvelopeOpen)
	closeAt := strings.Index(text[contentStart:], contextEnvelopeClose)
	if closeAt < 0 || referenceCodeMask(text[:start] + "\n\nsnapshot")[start+2] {
		return text, nil
	}
	contentEnd := contentStart + closeAt
	end := contentEnd + len(contextEnvelopeClose)
	own := text[:start] + text[end:]
	var snapshots []referenceSnapshot
	data := text[contentStart:contentEnd]
	if json.Unmarshal([]byte(data), &snapshots) != nil || len(snapshots) == 0 {
		return text, nil
	}
	mentions := threadMentions(own)
	covered := map[ThreadRef]bool{}
	for _, s := range snapshots {
		found := false
		for _, m := range mentions {
			if m.Ref == s.Ref && m.Title == s.Title {
				found = true
				break
			}
		}
		if !s.Ref.valid() || !found {
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

func joinReferenceMessages(texts []string) string {
	var bodies []string
	var snapshots []referenceSnapshot
	for _, text := range texts {
		own, refs := splitReferenceContext(text)
		bodies = append(bodies, own)
		snapshots = append(snapshots, refs...)
	}
	out := strings.Join(bodies, "\n\n")
	if len(snapshots) > 0 {
		data, _ := json.MarshalIndent(snapshots, "", "  ")
		out += contextEnvelopeOpen + string(data) + contextEnvelopeClose
	}
	return out
}
