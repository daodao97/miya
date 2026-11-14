package nschat

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	re "github.com/nsgo-dev/nsgo/base/richeditor"
	"miya/internal/chat/model"
	"miya/internal/textutil"
)

type inlineToken struct {
	start, end        int
	label, text, kind string
}

func editorTokens(raw string, title func(model.ThreadRef) string, commands []model.ComposerChoice, knownFiles ...[]string) []inlineToken {
	var out []inlineToken
	end := 0
	if i := strings.IndexAny(raw, " \t\r\n"); i > 1 && raw[0] == '/' {
		for _, c := range commands {
			if raw[1:i] == c.Name {
				out = append(out, inlineToken{0, i, raw[:i], raw[:i], "command"})
				end = i
				break
			}
		}
	}
	mentions := model.Mentions(raw)
	mask := model.ReferenceCodeMask(raw)
	for i := end; i < len(raw); {
		found := false
		for _, m := range mentions {
			if m.Start == i {
				out = append(out, inlineToken{i, m.End, textutil.ClipRunes(mentionLabel(m.Part, title), 24), raw[i:m.End], "reference"})
				i = m.End
				found = true
				break
			}
		}
		if found {
			continue
		}
		// A delimiter confirms file references; partial completion queries remain text.
		previous, _ := utf8.DecodeLastRuneInString(raw[:i])
		if raw[i] == '@' && (i == 0 || unicode.IsSpace(previous)) && !mask[i] {
			z := i + 1
			if z < len(raw) && raw[z] == '"' {
				z++
				for z < len(raw) {
					if raw[z] == '\\' && z+1 < len(raw) {
						z += 2
						continue
					}
					if raw[z] == '"' {
						z++
						break
					}
					z++
				}
			} else {
				for z < len(raw) {
					next, size := utf8.DecodeRuneInString(raw[z:])
					if unicode.IsSpace(next) {
						break
					}
					z += size
				}
			}
			path := strings.Trim(raw[i+1:z], "\"")
			known := strings.ContainsAny(path, "./\\")
			for _, files := range knownFiles {
				known = known || slices.Contains(files, path)
			}
			next, _ := utf8.DecodeRuneInString(raw[z:])
			if known && z > i+1 && z < len(raw) && unicode.IsSpace(next) {
				out = append(out, inlineToken{i, z, textutil.ClipRunes(raw[i:z], 24), raw[i:z], "file"})
				i = z
				continue
			}
		}
		i++
	}
	return out
}

func editorNodes(schema *re.Schema, raw string, tokens []inlineToken) []*re.Node {
	var out []*re.Node
	text := func(s string) {
		if s != "" {
			out = append(out, schema.Text(s))
		}
	}
	end := 0
	for _, t := range tokens {
		text(raw[end:t.start])
		out = append(out, schema.Nodes[t.kind].Create(re.Attrs{"label": t.label, "text": t.text}, nil, nil))
		end = t.end
	}
	text(raw[end:])
	return out
}
func editorPos(raw string, tokens []inlineToken, offset int) int {
	pos, end := 1, 0
	for _, t := range tokens {
		if offset <= t.start {
			return pos + len(utf16.Encode([]rune(raw[end:offset])))
		}
		pos += len(utf16.Encode([]rune(raw[end:t.start])))
		if offset < t.end {
			return pos + 1
		}
		pos++
		end = t.end
	}
	return pos + len(utf16.Encode([]rune(raw[end:offset])))
}
func editorOffset(doc *re.Node, pos int) int { return len(doc.TextBetween(1, max(1, pos), "", "")) }

// atomize changes only textual ranges, retaining existing atoms and selection.
func atomize(st *re.State, title func(model.ThreadRef) string, commands []model.ComposerChoice, files []string) *re.Transaction {
	value := re.PlainText(st.Doc)
	ts := editorTokens(value, title, commands, files)
	tr := st.Tr()
	changed := false
	for i := len(ts) - 1; i >= 0; i-- {
		t := ts[i]
		from, to := editorPos(value, nil, t.start), editorPos(value, nil, t.end)
		// Map wire offsets across the atoms already present in this document.
		at, wire := 1, 0
		hasAtom := false
		for _, n := range st.Doc.FirstChild().Content.Content {
			ntext := n.TextContent()
			if n.IsText() {
				ntext = n.Text
			}
			if n.IsLeaf() && !n.IsText() {
				ntext = n.AttrStr("text")
			}
			z := wire + len(ntext)
			if !n.IsText() && wire < t.end && z > t.start {
				hasAtom = true
			}
			if t.start >= wire && t.start < z {
				from = at
				if n.IsText() {
					from += len(utf16.Encode([]rune(ntext[:t.start-wire])))
				}
			}
			if t.end > wire && t.end <= z {
				to = at + n.NodeSize()
				if n.IsText() {
					to = at + len(utf16.Encode([]rune(ntext[:t.end-wire])))
				}
			}
			at += n.NodeSize()
			wire = z
		}
		if hasAtom {
			continue
		}
		if to-from > 1 || st.Doc.NodeAt(from) == nil || st.Doc.NodeAt(from).IsText() {
			tr.ReplaceWith(from, to, st.Schema.Nodes[t.kind].Create(re.Attrs{"label": t.label, "text": t.text}, nil, nil))
			changed = true
		}
	}
	if changed {
		return tr
	}
	return nil
}
