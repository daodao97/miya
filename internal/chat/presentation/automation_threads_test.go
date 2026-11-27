package presentation

import (
	"testing"

	"miya/internal/chat/model"
)

// Settled automation runs leave the project list; runs that still matter and
// conversations the person started stay, in their order.
func TestSplitAutomationThreads(t *testing.T) {
	all := []model.Thread{
		{ID: 1},
		{ID: 2, Automation: "a1"},
		{ID: 3, Automation: "a1", Busy: true},
		{ID: 4},
		{ID: 5, Automation: "a2"},
	}
	threads, runs := SplitAutomationThreads(all, func(t model.Thread) bool { return t.Busy || t.ID == 5 })
	if ids(threads) != "1,3,4,5" || ids(runs) != "2" {
		t.Fatalf("threads %s, runs %s", ids(threads), ids(runs))
	}
	threads, runs = SplitAutomationThreads(all[:1], func(model.Thread) bool { return false })
	if ids(threads) != "1" || len(runs) != 0 {
		t.Fatalf("threads %s, runs %s", ids(threads), ids(runs))
	}
}

func ids(threads []model.Thread) string {
	s := ""
	for i, t := range threads {
		if i > 0 {
			s += ","
		}
		s += string(rune('0' + t.ID))
	}
	return s
}
