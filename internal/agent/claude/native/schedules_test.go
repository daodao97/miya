package claude

import (
	"encoding/json"
	"testing"
	"time"
)

func scheduleFrame(t *testing.T, tracker *scheduleTracker, raw string) Frame {
	t.Helper()
	var f Frame
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	tracker.apply(&f)
	return f
}

func TestScheduleExpiryAndDynamicFallbackEndRetention(t *testing.T) {
	var s scheduleTracker
	s.set(ScheduledTask{ID: "expired", Status: "waiting", Recurring: true, NextFireAt: 1, CreatedAt: time.Now().Add(-8 * 24 * time.Hour).UnixMilli()})
	scheduleFrame(t, &s, `{"type":"command_lifecycle","state":"started","wakeup_source":"schedule_wakeup"}`)
	scheduleFrame(t, &s, `{"type":"result"}`)
	if s.active() {
		t.Fatal("expired recurring job retained the process")
	}
	s.set(ScheduledTask{ID: "wakeup", Status: "waiting", Recurring: true, NextFireAt: 1})
	for range 2 {
		scheduleFrame(t, &s, `{"type":"command_lifecycle","state":"started","wakeup_source":"schedule_wakeup"}`)
		scheduleFrame(t, &s, `{"type":"result"}`)
	}
	if s.active() {
		t.Fatal("dynamic loop retained after its fallback ended")
	}
}

func TestSchedulesUseSuccessfulNativeMetadata(t *testing.T) {
	var s scheduleTracker
	scheduleFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"CronCreate","input":{"cron":"*/5 * * * *","prompt":"check deploy"}}]}}`)
	if s.active() {
		t.Fatal("tool invocation alone must not create a schedule")
	}
	f := scheduleFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"scheduled"}]},"tool_use_result":{"id":"12345678","recurring":true,"nextFireAt":1793539210086.377}}`)
	if !f.SchedulesKnown || len(f.Schedules) != 1 || f.Schedules[0].NextFireAt != 1793539210086 || f.Schedules[0].Prompt != "check deploy" {
		t.Fatalf("native metadata: %+v", f.Schedules)
	}
	// A sub-agent's scheduling tools must not keep its parent session alive.
	scheduleFrame(t, &s, `{"type":"assistant","parent_tool_use_id":"child","message":{"content":[{"type":"tool_use","id":"b","name":"CronCreate","input":{"prompt":"child"}}]}}`)
	scheduleFrame(t, &s, `{"type":"user","parent_tool_use_id":"child","message":{"content":[{"type":"tool_result","tool_use_id":"b"}]},"tool_use_result":{"id":"child"}}`)
	if len(s.tasks) != 1 {
		t.Fatal("child schedule leaked")
	}
	scheduleFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"list","name":"CronList","input":{}}]}}`)
	scheduleFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"list"}]},"tool_use_result":{"jobs":[]}}`)
	if s.active() || s.tasks[0].Status != "stopped" {
		t.Fatal("removed job remained active")
	}
}

func TestDynamicLoopCompletionAndFailedSchedule(t *testing.T) {
	var s scheduleTracker
	scheduleFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"ScheduleWakeup","input":{"prompt":"watch CI","reason":"waiting on CI","delaySeconds":60}}]}}`)
	scheduleFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a"}]},"tool_use_result":{"scheduledFor":1793539080000}}`)
	if !s.active() || s.tasks[0].NextFireAt != 1793539080000 {
		t.Fatal("dynamic wakeup missing")
	}
	scheduleFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b","name":"ScheduleWakeup","input":{"stop":true}}]}}`)
	scheduleFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b"}]},"tool_use_result":{"stopped":true}}`)
	if s.active() || s.tasks[0].Status != "completed" {
		t.Fatal("dynamic loop did not complete")
	}
	scheduleFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"bad","name":"CronCreate","input":{"cron":"bad"}}]}}`)
	scheduleFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"bad","is_error":true}]},"tool_use_result":{"id":"bad"}}`)
	if s.active() {
		t.Fatal("failed schedule kept the process alive")
	}
}
