package integration

import (
	"encoding/json"
	"math"
	"miya/internal/agent/account"
	"miya/internal/appmodel"
	"miya/internal/controller"
	"strings"
	"testing"
)

func TestPhoneQuotasOnlyAccountUsage(t *testing.T) {
	week, short := 10080, 300
	reset := int64(1793244000)
	weekly := &account.QuotaWindow{WindowDurationMins: &week, UsedPercent: 81, ResetsAt: &reset}
	c := &controller.Controller{QuotaLimits: account.Limits{AccountID: "private-account", ByID: map[string]account.QuotaSnapshot{
		"spark": {Primary: weekly},
	}}}
	if len(c.PhoneQuotas()) != 0 {
		t.Fatal("model-only limits must not become agent quota")
	}
	c.QuotaLimits.ByID[appmodel.CodexAccountLimitID] = account.QuotaSnapshot{Primary: weekly, Secondary: &account.QuotaWindow{WindowDurationMins: &short}}
	quotas := c.PhoneQuotas()
	if len(quotas) != 1 || quotas[0].Agent != appmodel.AgentCodex || len(quotas[0].Windows) != 2 {
		t.Fatalf("unexpected quotas: %+v", quotas)
	}
	windows := quotas[0].Windows
	if windows[0].UsedPercent != 0 || windows[0].Label != "5-hour quota" || windows[1].UsedPercent != 81 || windows[1].ResetsAt != reset {
		t.Fatalf("zero usage, ordering or reset lost: %+v", windows)
	}
	b, err := json.Marshal(quotas)
	if err != nil || strings.Contains(string(b), "private-account") {
		t.Fatalf("invalid public quota metadata: %s, %v", b, err)
	}
	// Unknown-duration account windows are still real quota data.
	c.QuotaLimits = account.Limits{RateLimits: &account.QuotaSnapshot{Primary: &account.QuotaWindow{UsedPercent: 120}, Secondary: &account.QuotaWindow{UsedPercent: math.NaN()}}}
	quotas = c.PhoneQuotas()
	if len(quotas) != 1 || len(quotas[0].Windows) != 1 || quotas[0].Windows[0].UsedPercent != 100 {
		t.Fatalf("quota sanitization failed: %+v", quotas)
	}
}
