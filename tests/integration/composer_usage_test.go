package integration

import (
	"strings"
	"testing"

	"miya/internal/account"
	"miya/internal/appmodel"
	desktopui "miya/internal/ui"
	"miya/internal/widgets"
)

func TestComposerUsageEventMapping(t *testing.T) {
	c := testController()
	c.Handle(event("thread/tokenUsage/updated", `{"threadId":"t1","tokenUsage":{"last":{"totalTokens":25000,"inputTokens":20000,"cachedInputTokens":15000},"total":{"totalTokens":123000,"inputTokens":100000,"cachedInputTokens":70000,"outputTokens":23000,"reasoningOutputTokens":3000},"modelContextWindow":100000}}`))
	u := c.Threads()[0].Usage
	if u == nil || desktopui.UsageSummary(u) != "Context 25%" || c.Threads()[1].Usage != nil {
		t.Fatal("incorrect usage routing or context")
	}
	c.Handle(event("thread/tokenUsage/updated", `{"threadId":"t1","tokenUsage":null}`))
	if c.Threads()[0].Usage != u {
		t.Fatal("null event erased usage")
	}
}

func TestComposerQuotaPriority(t *testing.T) {
	h, d, w := 300, 7200, 10080
	fiveHour := &account.QuotaWindow{WindowDurationMins: &h, UsedPercent: 20}
	fiveDay := &account.QuotaWindow{WindowDurationMins: &d, UsedPercent: 40}
	week := &account.QuotaWindow{WindowDurationMins: &w, UsedPercent: 70}
	for _, tc := range []struct {
		q     account.QuotaSnapshot
		first *account.QuotaWindow
	}{
		{account.QuotaSnapshot{Primary: week, Secondary: fiveHour}, fiveHour},
		{account.QuotaSnapshot{Primary: week, Secondary: fiveDay}, fiveDay},
	} {
		windows := appmodel.ComposerQuotaWindows(account.Limits{RateLimits: &tc.q})
		if len(windows) != 2 || windows[0] != tc.first {
			t.Fatal("wrong ring quota priority")
		}
	}
	if len(appmodel.ComposerQuotaWindows(account.Limits{ByID: map[string]account.QuotaSnapshot{"spark": {Primary: fiveHour}}})) != 0 {
		t.Fatal("model quota used as account quota")
	}
	if strings.Contains(widgets.QuotaRing(0), "<path") || !strings.Contains(widgets.QuotaRing(50), "<path") {
		t.Fatal("invalid ring progress")
	}
}
