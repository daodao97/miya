package codex

import "encoding/json"

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
	ID     string `json:"id"`
	Status string `json:"status"`
	Items  []Item `json:"items"`
	Error  *struct {
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
type Approval struct {
	Permissions     json.RawMessage `json:"permissions"`
	Payload         json.RawMessage
	Questions       []Question      `json:"questions"`
	Message         string          `json:"message"`
	Mode            string          `json:"mode"`
	URL             string          `json:"url"`
	ServerName      string          `json:"serverName"`
	RequestedSchema json.RawMessage `json:"requestedSchema"`
	ID              json.RawMessage
	Method          string
	ThreadID        string `json:"threadId"`
	TurnID          string `json:"turnId"`
	ItemID          string `json:"itemId"`
	Command         string `json:"command"`
	Cwd             string `json:"cwd"`
	Reason          string `json:"reason"`
	GrantRoot       string `json:"grantRoot"`
}

type Question struct {
	ID       string `json:"id"`
	Header   string `json:"header"`
	Question string `json:"question"`
	IsSecret bool   `json:"isSecret"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// TokenUsage is supplied by the Codex app-server, not estimated locally.
type TokenUsage struct {
	Last               TokenBreakdown `json:"last"`
	Total              TokenBreakdown `json:"total"`
	ModelContextWindow *int           `json:"modelContextWindow"`
}
type TokenBreakdown struct {
	TotalTokens           int  `json:"totalTokens"`
	InputTokens           *int `json:"inputTokens"`
	CachedInputTokens     *int `json:"cachedInputTokens"`
	OutputTokens          *int `json:"outputTokens"`
	ReasoningOutputTokens *int `json:"reasoningOutputTokens"`
}
