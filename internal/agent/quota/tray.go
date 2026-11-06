package quota

import (
	"fmt"
	"math"
	"strings"

	agentmeta "miya/internal/agent/catalog"
)

func RemainingUsage(used float64) int { return int(math.Round(100 - Percent(used))) }

func SelectedUsage(readings []Reading, agent string) *Reading {
	for _, d := range agentmeta.Usage() {
		candidate := d.UsageID
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

func AgentName(id string) string { return agentmeta.UsageFor(id).UsageName }

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
