//go:build !mygo_ui

package integration

import (
	"miya/internal/chat/model"
	mobile "miya/internal/phone/client"
	phoneserver "miya/internal/phone/server"
	"testing"
)

func TestClaudeBackgroundShellPhoneSyncWithoutNewMessages(t *testing.T) {
	thread := longThread(2)
	thread.Agent = "claude-code"
	phone := mobile.New()
	sync := phoneserver.NewThreadSync(nil)
	initial, _ := sync.Update(thread)
	phoneOf(t, phone, initial)
	for _, tasks := range [][]model.BackgroundTask{{{ID: "deploy", Description: "npm run deploy"}}, nil} {
		thread.BackgroundTasks = tasks
		delta, changed := sync.Update(thread)
		if !changed || len(delta.Changed) != 0 {
			t.Fatal("background state did not sync independently of message changes")
		}
		phoneOf(t, phone, delta)
		got := phone.Viewer.Current()
		if len(got.BackgroundTasks) != len(tasks) || !sameMessages(got.Messages, thread.Messages) {
			t.Fatalf("phone lost background state or message history: %+v", got)
		}
	}
}
