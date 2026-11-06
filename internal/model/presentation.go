package model

// MessagePresentation is the renderer contract. Native items remain available
// to persistence/execution code; renderers need only these normalized fields.
type MessagePresentation struct {
	Content string                `json:"content"`
	Title   string                `json:"title,omitempty"`
	Icon    string                `json:"icon,omitempty"`
	Format  string                `json:"format,omitempty"` // text, markdown, diff or files
	Changes []PresentedFileChange `json:"changes,omitempty"`
	Plan    []PresentedPlanStep   `json:"plan,omitempty"`
}
type PresentedFileChange struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
}
type PresentedPlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}
