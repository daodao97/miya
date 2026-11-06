package agentusage

import (
	"fmt"
	"math"
	"strings"
)

func RemainingUsage(used float64) int { return int(math.Round(100 - Percent(used))) }

func SelectedUsage(readings []Reading, agent string) *Reading {
	for _, candidate := range []string{"codex", "claude"} {
		if agent != "" && agent != candidate {
			continue
		}
		for i := range readings {
			r := &readings[i]
			if r.Agent == candidate && r.Error == "" && len(r.Windows) > 0 {
				return r
			}
		}
	}
	return nil
}

func AgentName(agent string) string {
	if agent == "claude" {
		return "Claude"
	}
	return "Codex"
}

func TrayTitle(r *Reading) string {
	if r == nil || len(r.Windows) == 0 {
		return "—"
	}
	labels := make([]string, 0, 2)
	for _, w := range r.Windows[:min(2, len(r.Windows))] {
		labels = append(labels, fmt.Sprintf("%d%%", RemainingUsage(w.Used)))
	}
	return strings.Join(labels, " · ")
}
