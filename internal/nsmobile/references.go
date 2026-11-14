package nsmobile

import (
	"strconv"
	"strings"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/remote"

	"github.com/nsgo-dev/nsgo/core"
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
func (m *App) references(cx *core.Scope) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		m.revision.Get()
		end := m.caret
		if end < 0 || end > len(m.Draft) {
			end = len(m.Draft)
		}
		token := composer.ComposerTokenAt(m.Draft, end)
		var nodes []core.Node
		// Agent/delegation tags are intentionally deferred with delegations.
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
