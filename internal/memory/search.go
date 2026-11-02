package memory

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func normalized(s string) string { return strings.ToLower(norm.NFKC.String(s)) }
func terms(s string) []string {
	s = normalized(s)
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	var word, han []rune
	flushWord := func() { add(string(word)); word = nil }
	flushHan := func() {
		if len(han) == 1 {
			add(string(han))
		}
		for i := 1; i < len(han); i++ {
			add(string(han[i-1 : i+1]))
		}
		han = nil
	}
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			flushHan()
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return out
}

// Search is keyword retrieval, scoped before ranking. It never calls a model.
// Empty or unrelated queries return no entries. Management uses List instead.
func (s *Store) Search(project Scope, query, task string, limit int) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(project)
	if err != nil {
		return nil, err
	}
	if d.Settings.Disabled {
		return []Entry{}, nil
	}
	docs := []document{d}
	if d.Settings.UseUserPreferences {
		user, err := s.load(UserScope())
		if err != nil {
			return nil, err
		}
		docs = append(docs, user)
	}
	tokens := terms(query)
	if len(tokens) == 0 && strings.TrimSpace(task) == "" {
		return []Entry{}, nil
	}
	type hit struct {
		e     Entry
		score int
	}
	hits := []hit{}
	for _, doc := range docs {
		for _, e := range latest(doc) {
			e = effective(e, s.now())
			if e.State != "active" || doc.Scope.Kind == "user" && e.Kind != "preference" {
				continue
			}
			if e.Kind == "progress" && (task == "" || e.TaskKey != task) {
				continue
			}
			content := normalized(e.Content)
			words := map[string]bool{}
			for _, w := range terms(e.Content) {
				words[w] = true
			}
			score := 0
			for _, w := range tokens {
				if words[w] {
					score += 2
				}
			}
			if q := strings.TrimSpace(normalized(query)); q != "" && strings.Contains(content, q) {
				score += 5
			}
			if task != "" && e.TaskKey == task {
				score += 8
			}
			if score > 0 {
				hits = append(hits, hit{e, score})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if !hits[i].e.ObservedAt.Equal(hits[j].e.ObservedAt) {
			return hits[i].e.ObservedAt.After(hits[j].e.ObservedAt)
		}
		return hits[i].e.ID < hits[j].e.ID
	})
	if limit <= 0 || limit > 5 {
		limit = 5
	}
	out := []Entry{}
	budget := 4000
	for _, h := range hits {
		// Retrieval sends compact source locators; full evidence excerpts remain
		// available in management. Repeated corrections must not consume the
		// entire recall budget with their historical quotes.
		for i := range h.e.Sources {
			h.e.Sources[i].Quote = ""
		}
		cost := utf8.RuneCountInString(h.e.Content) + len(h.e.ID) + 80
		for _, src := range h.e.Sources {
			cost += utf8.RuneCountInString(src.Quote) + utf8.RuneCountInString(src.Thread) + 80
		}
		if cost > budget {
			continue
		}
		budget -= cost
		out = append(out, h.e)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
