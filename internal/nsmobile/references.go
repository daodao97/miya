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
func (m *App) selectedRange() core.TextRange {
	a, b := m.anchor, m.caret
	if b < 0 || b > len(m.Draft) {
		a, b = len(m.Draft), len(m.Draft)
	}
	return core.TextRange{Start: byteUTF16Offset(m.Draft, max(0, a)), End: byteUTF16Offset(m.Draft, b)}
}
func (m *App) references(cx *core.Scope) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		m.revision.Get()
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
					m.Draft = composer.InsertAgentReference(m.Draft, token, choice.Agent, choice.Name)
					m.caret = -1
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
						m.Draft = composer.InsertThreadReference(m.Draft, token, ref, title)
						m.caret = -1
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
				nodes = append(nodes, m.button("file-reference-"+choice.Name, choice.Name, nil, func() { m.Draft = composer.InsertFileReference(m.Draft, token, choice.Name); m.caret = -1 }))
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
					nodes = append(nodes, m.button("command-"+choice.Name, "/"+choice.Name, nil, func() { m.Draft = "/" + choice.Name + " "; m.caret = -1 }))
				}
			}
		}
		if len(nodes) == 0 {
			return core.View{}
		}
		return core.Scroll{TestID: "composer-references", Style: core.Style{MaxHeight: core.Pt(220)}, Children: []core.Node{column(nodes...)}}
	})
}
