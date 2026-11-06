//go:build !windows

package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"miya/internal/chat/model"
)

func TestCodexGoalCommandsUseNativeRPC(t *testing.T) {
	c, d, log := codexResumeFixture(t)
	c.Update(1, func(t *model.Thread) { t.Resumed = false })
	id, msg := c.CodexGoalCommand(model.SendRequest{ThreadID: 1, Text: "/goal --budget 50000 fix CI"})
	if id != 1 || msg != "" {
		t.Fatalf("%d %s", id, msg)
	}
	waitCodexResume(t, c, d, func() bool { return !c.ThreadWithID(1).TaskPending })
	if tasks := c.ThreadWithID(1).Tasks; len(tasks) != 1 || tasks[0].Title != "fix CI" || *tasks[0].TokenBudget != 50000 {
		t.Fatalf("%+v", tasks)
	}
	for _, action := range []string{"pause", "resume", "clear"} {
		if msg := c.TaskAction(1, "goal", action); msg != "" {
			t.Fatal(msg)
		}
		waitCodexResume(t, c, d, func() bool { return !c.ThreadWithID(1).TaskPending })
	}
	if len(c.ThreadWithID(1).Tasks) != 0 {
		t.Fatal("clear retained goal")
	}
	sets, turns := 0, 0
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r struct {
			Method string
			Params json.RawMessage
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		switch r.Method {
		case "thread/goal/set":
			sets++
			var p map[string]any
			_ = json.Unmarshal(r.Params, &p)
			if p["origin"] != "user" {
				t.Fatal("goal mutation lost user provenance")
			}
		case "turn/start":
			turns++
		}
	}
	if sets != 3 || turns != 0 {
		t.Fatalf("sets=%d turn/start=%d", sets, turns)
	}
}
