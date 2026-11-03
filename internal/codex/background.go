package codex

import (
	"context"
	"errors"
)

type BackgroundTerminal struct {
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	ItemID    string `json:"itemId"`
	ProcessID string `json:"processId"`
	OSPID     *int   `json:"osPid"`
}

// BackgroundTerminals publishes a set only after every page succeeds.
func (c *Client) BackgroundTerminals(ctx context.Context, threadID string) ([]BackgroundTerminal, error) {
	var out []BackgroundTerminal
	cursor := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Data       []BackgroundTerminal `json:"data"`
			NextCursor *string              `json:"nextCursor"`
		}
		args := map[string]any{"threadId": threadID, "limit": 100}
		if cursor != "" {
			args["cursor"] = cursor
		}
		if err := c.Call(ctx, "thread/backgroundTerminals/list", args, &page); err != nil {
			return nil, err
		}
		if page.Data == nil {
			return nil, errors.New("Codex background terminal snapshot is missing data")
		}
		out = append(out, page.Data...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		cursor = *page.NextCursor
		if seen[cursor] {
			return nil, errors.New("Codex background terminal cursor repeated")
		}
		seen[cursor] = true
	}
}
