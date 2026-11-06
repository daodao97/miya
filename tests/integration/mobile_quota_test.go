package integration

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/account"
	"miya/internal/appmodel"
	"miya/internal/controller"
	"miya/internal/phoneapi"
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

func TestMobileDrawerQuotaInspection(t *testing.T) {
	m := testMobileApp()
	m.State.Agent = appmodel.AgentClaude
	m.State.Quotas = []phoneapi.AgentQuota{{Agent: appmodel.AgentCodex, Windows: []phoneapi.QuotaWindow{{Label: "Weekly: ", UsedPercent: 81}}}}
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if !tt.HasText("19") {
		t.Fatal("quota ring should show the remaining percentage")
	}
	saveMobileNativeSnapshot(t, tt, "drawer-quota-remaining")
	if _, ok := tt.Find("Claude quota"); ok {
		t.Fatal("missing agent quota must be hidden")
	}
	if err := tt.Click("Codex quota"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Weekly: ") || !tt.HasText("81% used") {
		t.Fatal("tap should show quota details")
	}
	if m.State.Agent != appmodel.AgentClaude || !m.Menu {
		t.Fatal("inspecting quota changed agent or navigation")
	}
	m.State.Quotas = nil
	tt.Frame()
	if _, ok := tt.Find("Codex quota"); ok {
		t.Fatal("unavailable quota must be hidden")
	}
	if m.QuotaOpen || tt.HasText("81% used") {
		t.Fatal("removed quota left its popover open")
	}
}
