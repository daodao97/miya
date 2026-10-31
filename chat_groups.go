package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type messageGroup struct {
	ID            int
	User          bool
	TurnID        string
	Messages      []Message
	Streaming     bool
	Timestamp     int64
	ShowTimestamp bool
}

const collapsedThreadCount = 5

// displayedThreads keeps a busy project sidebar compact while preserving the
// newest conversations. The full list remains owned by the controller.
func displayedThreads(all []Thread, expanded bool) []Thread {
	all = append([]Thread(nil), all...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].LastActivityAt > all[j].LastActivityAt })
	if expanded || len(all) <= collapsedThreadCount {
		return all
	}
	return all[:collapsedThreadCount]
}

func threadMessageGroups(t Thread, groups []messageGroup) []messageGroup {
	if !t.Busy {
		return groups
	}
	for i := range groups {
		if groups[i].User {
			continue
		}
		if t.TurnID == "" {
			groups[i].Streaming = i == len(groups)-1
			continue
		}
		for _, m := range groups[i].Messages {
			if m.TurnID == t.TurnID {
				groups[i].Streaming = true
				break
			}
		}
	}
	return groups
}

// Group protocol items for presentation without merging or modifying their data.
// Tool activity belongs to the same response; a user message or new turn separates it.
func groupMessages(messages []Message) []messageGroup {
	var groups []messageGroup
	for _, m := range messages {
		// Reasoning stays in the thread model, but is not a visible timeline item.
		// Filter before grouping so it creates neither empty responses nor counts.
		if strings.EqualFold(m.Kind, "reasoning") {
			continue
		}
		user := m.Role == "user"
		newGroup := len(groups) == 0 || user
		if !newGroup {
			last := groups[len(groups)-1]
			// The app-server may emit assistant and activity items with different
			// item/turn metadata while one response is still being assembled. Keep
			// those pieces in one response group; only two consecutive assistant
			// messages with different turns start a new response.
			lastRole := ""
			if len(last.Messages) > 0 {
				lastRole = last.Messages[len(last.Messages)-1].Role
			}
			newGroup = last.User || (m.Role == "assistant" && lastRole == "assistant" && m.TurnID != "" && last.TurnID != "" && m.TurnID != last.TurnID)
		}
		if newGroup {
			groups = append(groups, messageGroup{ID: m.ID, User: user, TurnID: m.TurnID})
		}
		g := &groups[len(groups)-1]
		if g.TurnID == "" {
			g.TurnID = m.TurnID
		}
		g.Messages = append(g.Messages, m)
	}
	for i := range groups {
		for _, m := range groups[i].Messages {
			if m.SentAtMs > 0 {
				groups[i].Timestamp = m.SentAtMs
				break
			}
		}
		if groups[i].Timestamp == 0 {
			continue
		}
		if i == 0 || groups[i-1].Timestamp == 0 || groups[i].Timestamp-groups[i-1].Timestamp >= 10*60*1000 {
			groups[i].ShowTimestamp = true
		}
	}
	return groups
}

func messageGroupText(g messageGroup) string {
	var parts []string
	for _, m := range g.Messages {
		if m.Content != "" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func formatMessageTimestamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).In(time.Local)
	now := time.Now().In(time.Local)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("3:04 PM")
	}
	if now.After(t) && now.Sub(t) < 7*24*time.Hour {
		return t.Format("Monday 3:04 PM")
	}
	return t.Format("Jan 2, 2006 3:04 PM")
}

func turnElapsedLabel(messages []Message, active bool) string {
	var start, end int64
	for _, m := range messages {
		if m.StartedAtMs > 0 && (start == 0 || m.StartedAtMs < start) {
			start = m.StartedAtMs
		}
		if m.CompletedAtMs > end {
			end = m.CompletedAtMs
		}
	}
	if start == 0 {
		return ""
	}
	if end == 0 {
		end = time.Now().UnixMilli()
	}
	verb := tr("Worked for ")
	if active {
		verb = tr("Working for ")
	}
	return verb + formatElapsed(end-start)
}

func formatElapsed(ms int64) string {
	if ms < 1000 {
		return "under 1s"
	}
	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if minutes < 60 {
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
}

func threadWorkingLabel(t Thread) string {
	if label := threadReconnectLabel(t); label != "" {
		return label
	}
	if t.StartedAtMs > 0 {
		return tr("Working for ") + formatElapsed(time.Now().UnixMilli()-t.StartedAtMs)
	}
	if t.Status != "" {
		return tr(t.Status)
	}
	return tr("Waiting for agent…")
}

var reconnectAttempt = regexp.MustCompile(`\b\d+\s*/\s*\d+\b`)

// Retry status is also understood by older desktop and phone versions: it
// travels in the existing thread metadata, including updates with no text.
func threadReconnectLabel(t Thread) string {
	if !t.Busy || !strings.HasPrefix(t.Status, "Retrying: ") {
		return ""
	}
	label := tr("Reconnecting…")
	if attempt := reconnectAttempt.FindString(t.Status); attempt != "" {
		label += " " + strings.ReplaceAll(attempt, " ", "")
	}
	return label
}

// Group only consecutive activities; commentary and answers retain their order.
type activityRun struct {
	ID       int
	Activity bool
	Messages []Message
}

func activityRuns(messages []Message) []activityRun {
	var runs []activityRun
	for _, m := range messages {
		activity := m.Role == "tool" || m.Role == "activity"
		if !activity || len(runs) == 0 || !runs[len(runs)-1].Activity {
			runs = append(runs, activityRun{ID: m.ID, Activity: activity})
		}
		runs[len(runs)-1].Messages = append(runs[len(runs)-1].Messages, m)
	}
	return runs
}
