package protocol

type TerminalFrame struct {
	Op                        string `json:"op"`
	Project                   string `json:"project,omitempty"`
	Data                      []byte `json:"data,omitempty"`
	Cols, Rows, Width, Height int
	Error                     string `json:"error,omitempty"`
}
