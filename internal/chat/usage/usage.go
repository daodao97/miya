// Package tokenusage holds observed token accounting shared by agent integrations.
package usage

// Usage is reported by an agent, never estimated by the view.
type Usage struct {
	Last               Breakdown `json:"last"`
	Total              Breakdown `json:"total"`
	ModelContextWindow *int      `json:"modelContextWindow"`
}
type Breakdown struct {
	TotalTokens           int  `json:"totalTokens"`
	InputTokens           *int `json:"inputTokens"`
	CachedInputTokens     *int `json:"cachedInputTokens"`
	OutputTokens          *int `json:"outputTokens"`
	ReasoningOutputTokens *int `json:"reasoningOutputTokens"`
}
