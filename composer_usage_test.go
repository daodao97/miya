package main

import (
	"strings"
	"testing"
)

func TestComposerUsageEventMapping(t *testing.T) {
	c := testController()
	c.handle(event("thread/tokenUsage/updated", `{"threadId":"t1","tokenUsage":{"last":{"totalTokens":25000,"inputTokens":20000,"cachedInputTokens":15000},"total":{"totalTokens":123000,"inputTokens":100000,"cachedInputTokens":70000,"outputTokens":23000,"reasoningOutputTokens":3000},"modelContextWindow":100000}}`))
	u := c.threads()[0].Usage
	if u == nil || usageSummary(u) != "上下文 25%" || c.threads()[1].Usage != nil {
		t.Fatal("incorrect usage routing or context")
	}
	c.handle(event("thread/tokenUsage/updated", `{"threadId":"t1","tokenUsage":null}`))
	if c.threads()[0].Usage != u {
		t.Fatal("null event erased usage")
	}
}

func TestComposerQuotaPriority(t *testing.T) {
	h, d, w := 300, 7200, 10080
	fiveHour := &quotaWindow{WindowDurationMins: &h, UsedPercent: 20}
	fiveDay := &quotaWindow{WindowDurationMins: &d, UsedPercent: 40}
	week := &quotaWindow{WindowDurationMins: &w, UsedPercent: 70}
	for _, tc := range []struct {
		q     quotaSnapshot
		first *quotaWindow
	}{
		{quotaSnapshot{Primary: week, Secondary: fiveHour}, fiveHour},
		{quotaSnapshot{Primary: week, Secondary: fiveDay}, fiveDay},
	} {
		windows := composerQuotaWindows(accountLimits{RateLimits: &tc.q})
		if len(windows) != 2 || windows[0] != tc.first {
			t.Fatal("wrong ring quota priority")
		}
	}
	if len(composerQuotaWindows(accountLimits{ByID: map[string]quotaSnapshot{"spark": {Primary: fiveHour}}})) != 0 {
		t.Fatal("model quota used as account quota")
	}
	if strings.Contains(quotaRing(0), "<path") || !strings.Contains(quotaRing(50), "<path") {
		t.Fatal("invalid ring progress")
	}
}
