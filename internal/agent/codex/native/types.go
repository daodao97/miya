package codex

import (
	"encoding/json"

	"miya/internal/interaction"
	"miya/internal/tokenusage"
)

type Model struct {
	ID                        string `json:"id"`
	Model                     string `json:"model"`
	DisplayName               string `json:"displayName"`
	IsDefault                 bool   `json:"isDefault"`
	DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}
type Item struct {
	Cwd               string          `json:"cwd"`
	ProcessID         string          `json:"processId"`
	ID                string          `json:"id"`
	Type              string          `json:"type"`
	Text              string          `json:"text"`
	Phase             string          `json:"phase,omitempty"`
	Content           json.RawMessage `json:"content"`
	Summary           []string        `json:"summary"`
	Server            string          `json:"server"`
	Tool              string          `json:"tool"`
	Query             string          `json:"query"`
	Path              string          `json:"path"`
	SavedPath         string          `json:"savedPath"`
	Review            string          `json:"review"`
	Arguments         json.RawMessage `json:"arguments"`
	Result            json.RawMessage `json:"result"`
	ToolError         json.RawMessage `json:"error"`
	AgentsStates      json.RawMessage `json:"agentsStates"`
	ReceiverThreadIDs []string        `json:"receiverThreadIds"`
	Prompt            string          `json:"prompt"`
	AgentPath         string          `json:"agentPath"`
	AgentThreadID     string          `json:"agentThreadId"`
	Kind              string          `json:"kind"`
	Output            json.RawMessage `json:"output"`
	Failure           json.RawMessage `json:"failure"`
	DurationMS        int64           `json:"durationMs"`
	ExitCode          *int            `json:"exitCode"`

	CommandActions []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Path  string `json:"path"`
		Query string `json:"query"`
	} `json:"commandActions"`
	Command          string `json:"command"`
	Status           string `json:"status"`
	AggregatedOutput string `json:"aggregatedOutput"`
	Changes          []struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	} `json:"changes"`
}
type Turn struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Items       []Item `json:"items"`
	StartedAt   int64  `json:"startedAt"`
	CompletedAt int64  `json:"completedAt"`
	DurationMS  *int64 `json:"durationMs"`
	Error       *struct {
		Message string `json:"message"`
	} `json:"error"`
}
type Thread struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Preview   string `json:"preview"`
	Cwd       string `json:"cwd"`
	UpdatedAt int64  `json:"updatedAt"`
	Turns     []Turn `json:"turns"`
}
type ThreadResponse struct {
	Thread Thread `json:"thread"`
	Model  string `json:"model"`
}
type PlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}
type Event struct {
	Goal        *Goal           `json:"goal"`
	RequestID   json.RawMessage `json:"requestId"`
	Diff        string          `json:"diff"`
	Explanation string          `json:"explanation"`
	Plan        []PlanStep      `json:"plan"`
	TokenUsage  *TokenUsage     `json:"tokenUsage"`

	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
	Item     Item   `json:"item"`
	Turn     Turn   `json:"turn"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
	WillRetry bool `json:"willRetry"`
}

// Goal is persisted and scheduled by the Codex app-server.
type Goal struct {
	ThreadID        string `json:"threadId"`
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	TokenBudget     *int64 `json:"tokenBudget"`
	TokensUsed      int64  `json:"tokensUsed"`
	TimeUsedSeconds int64  `json:"timeUsedSeconds"`
}

type GoalResponse struct {
	Goal *Goal `json:"goal"`
}

// Approval and Question keep the established native client API and wire shape.
// Interaction data belongs to the shared contract used by every adapter.
type Approval = interaction.Request
type Question = interaction.Question

type TokenUsage = tokenusage.Usage
type TokenBreakdown = tokenusage.Breakdown
