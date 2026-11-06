package widgets

import (
	automation "miya/internal/automation"
)

func LastAutomationRun(v automation.View) (automation.Run, bool) {
	for i := len(v.Runs) - 1; i >= 0; i-- {
		if v.Runs[i].Result != automation.Skipped {
			return v.Runs[i], true
		}
	}
	return automation.Run{}, false
}
