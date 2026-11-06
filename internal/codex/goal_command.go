package codex

import (
	"fmt"
	"strconv"
	"strings"
)

type GoalCommand struct {
	Action, Objective string
	Budget            *int64
}

func ParseGoalCommand(text string) (GoalCommand, bool, error) {
	name, args, _ := strings.Cut(strings.TrimSpace(text), " ")
	if name != "/goal" {
		return GoalCommand{}, false, nil
	}
	args = strings.TrimSpace(args)
	switch args {
	case "", "pause", "resume", "clear":
		return GoalCommand{Action: args}, true, nil
	}
	g := GoalCommand{Action: "set", Objective: args}
	if strings.HasPrefix(args, "--budget ") {
		value, objective, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(args, "--budget ")), " ")
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 || strings.TrimSpace(objective) == "" {
			return g, true, fmt.Errorf("Usage: /goal --budget <positive integer> <goal>")
		}
		g.Budget, g.Objective = &n, strings.TrimSpace(objective)
	}
	return g, true, nil
}
