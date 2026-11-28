package nsmobile

import (
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/remote"
)

// Native input selections use UTF-16 offsets; composer tokens use bytes.
func utf16ByteOffset(s string, offset int) int {
	units := 0
	for i, r := range s {
		if units >= offset {
			return i
		}
		units++
		if r > 0xffff {
			units++
		}
	}
	return len(s)
}

// byteUTF16Offset converts the model's caret back to the native input range.
func byteUTF16Offset(s string, offset int) int {
	units := 0
	for i, r := range s {
		if i >= offset {
			break
		}
		units++
		if r > 0xffff {
			units++
		}
	}
	return units
}

// setComposerDraft is for app-owned edits. Native edits only report the
// selection so that typing never moves the keyboard's caret from Go.
func (m *App) setComposerDraft(text string, anchor, caret int) {
	m.Draft, m.anchor, m.caret = text, anchor, caret
	if m.composerInput != nil && !m.composing {
		m.composerInput.SetSelection(byteUTF16Offset(text, anchor), byteUTF16Offset(text, caret))
	}
}

func (m *App) acceptComposerSuggestion(text string) {
	if !m.composing {
		m.setComposerDraft(text, len(text), len(text))
	}
}
func (m *App) references(cx *core.Scope) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		m.revision.Get()
		if m.composing {
			return nil
		}
		end := m.caret
		if end < 0 || end > len(m.Draft) {
			end = len(m.Draft)
		}
		token := composer.ComposerTokenAt(m.Draft, end)
		var nodes []core.Node
		if (token.Kind == '#' || token.Kind == '@') && m.State.Collaboration != nil && m.State.Collaboration.Version >= 1 {
			var enabled []string
			for _, d := range m.AgentDescriptors() {
				enabled = append(enabled, d.ID)
			}
			for _, choice := range composer.AgentChoices(token.Query, enabled) {
				nodes = append(nodes, m.button("agent-reference-"+choice.Agent, choice.Name, nil, func() {
					m.acceptComposerSuggestion(composer.InsertAgentReference(m.Draft, token, choice.Agent, choice.Name))
				}))
			}
		}
		if token.Kind == '@' {
			if m.State.ReferenceSource != "" {
				for _, ref := range appmodel.MatchingThreadRefs(m.State.Projects, m.Open, token.Query) {
					t := ref.Thread
					title := appmodel.ThreadRefTitle(t)
					nodes = append(nodes, m.button("reference-"+strconv.Itoa(t.ID), title, nil, func() {
						ref := model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}
						m.acceptComposerSuggestion(composer.InsertThreadReference(m.Draft, token, ref, title))
						m.Action(remote.Request{Op: "prefetch", Reference: ref.URL()})
					}))
				}
			}
			files := m.SearchFileReferences(m.ComposerProject(), token.Query)
			if files.Loading {
				nodes = append(nodes, text(tr("Searching project files…")))
			} else if files.Error != "" {
				nodes = append(nodes, text(files.Error))
				if m.State.FileReferences && m.Connected {
					nodes = append(nodes, m.button("retry-file-search", tr("Retry file search"), nil, func() { m.ClearFileReferences() }))
				}
			}
			for _, choice := range files.Choices {
				nodes = append(nodes, m.button("file-reference-"+choice.Name, choice.Name, nil, func() {
					m.acceptComposerSuggestion(composer.InsertFileReference(m.Draft, token, choice.Name))
				}))
			}
		}
		if strings.HasPrefix(m.Draft, "/") && !strings.ContainsAny(m.Draft, " \n") {
			choices, known := m.ComposerCommands()
			if !known {
				nodes = append(nodes, text(tr("Loading commands…")))
			}
			query := strings.ToLower(strings.TrimPrefix(m.Draft, "/"))
			for _, choice := range choices {
				if strings.Contains(strings.ToLower(choice.Name), query) {
					nodes = append(nodes, m.button("command-"+choice.Name, "/"+choice.Name, nil, func() {
						m.acceptComposerSuggestion("/" + choice.Name + " ")
					}))
				}
			}
		}
		if len(nodes) == 0 {
			return core.View{}
		}
		return core.Scroll{TestID: "composer-references", Style: core.Style{MaxHeight: core.Pt(220)}, Children: []core.Node{column(nodes...)}}
	})
}
