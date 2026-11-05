package automation

import (
	"encoding/json"
	"strings"

	"miya/internal/textutil"
)

// Each automation run ends with a structured result: the agent reports it
// with the miya report_result tool, or Miya makes one from the
// conversation's last reply. It is kept with the run, so the next run can
// compare against it after the conversation itself is gone.

// Report is a run's result.
type Report struct {
	Summary   string   `json:"summary"`
	Findings  []string `json:"findings,omitempty"`
	Metrics   []Metric `json:"metrics,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
	NeedsUser bool     `json:"needs_user,omitempty"`
	// Auto marks a result Miya made because the agent reported none.
	Auto bool `json:"auto,omitempty"`
}

// Metric is one measured value of a run, such as an amount or a share.
type Metric struct {
	Name  string     `json:"name"`
	Value flexString `json:"value"`
	Unit  string     `json:"unit,omitempty"`
}

// flexString takes a JSON string or number: agents send either.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	*f = flexString(strings.TrimSpace(string(b)))
	return nil
}

func (m Metric) Text() string {
	return strings.TrimSpace(m.Name) + ": " + strings.TrimSpace(string(m.Value)) + strings.TrimSpace(m.Unit)
}

const (
	ReportSummaryLimit = 2000
	ReportItemLimit    = 500
	ReportListLimit    = 30
)

// Cleaned bounds a reported result.
func (r Report) Cleaned() Report {
	list := func(items []string) []string {
		var out []string
		for _, s := range items {
			if s = strings.TrimSpace(s); s != "" && len(out) < ReportListLimit {
				out = append(out, textutil.ClipRunes(s, ReportItemLimit))
			}
		}
		return out
	}
	out := Report{Summary: textutil.ClipRunes(strings.TrimSpace(r.Summary), ReportSummaryLimit), Findings: list(r.Findings), Artifacts: list(r.Artifacts), NeedsUser: r.NeedsUser, Auto: r.Auto}
	for _, m := range r.Metrics {
		if strings.TrimSpace(m.Name) != "" && len(out.Metrics) < ReportListLimit {
			out.Metrics = append(out.Metrics, Metric{Name: textutil.ClipRunes(strings.TrimSpace(m.Name), 100), Value: flexString(textutil.ClipRunes(strings.TrimSpace(string(m.Value)), 100)), Unit: textutil.ClipRunes(strings.TrimSpace(m.Unit), 20)})
		}
	}
	return out
}

// LastReport is the result of an automation's latest run that has one.
func (s Store) LastReport(id string) (Run, bool) {
	runs := s.Runs[id]
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Report != nil && runs[i].Result != Running {
			return runs[i], true
		}
	}
	return Run{}, false
}

// FormatReport writes a result as a message reads it.
func FormatReport(r Report) string {
	var b strings.Builder
	b.WriteString("Summary: " + r.Summary + "\n")
	if len(r.Findings) > 0 {
		b.WriteString("Findings:\n")
		for _, f := range r.Findings {
			b.WriteString("- " + f + "\n")
		}
	}
	if len(r.Metrics) > 0 {
		b.WriteString("Metrics:\n")
		for _, m := range r.Metrics {
			b.WriteString("- " + m.Text() + "\n")
		}
	}
	if len(r.Artifacts) > 0 {
		b.WriteString("Artifacts: " + strings.Join(r.Artifacts, ", ") + "\n")
	}
	if r.NeedsUser {
		b.WriteString("User action was required.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
