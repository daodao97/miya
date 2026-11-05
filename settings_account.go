package main

import (
	"fmt"
)

type accountRead struct {
	Account *accountIdentity `json:"account"`
}

func quotaLabel(w *quotaWindow, index int) string {
	if w.WindowDurationMins != nil {
		m := *w.WindowDurationMins
		if m == 300 {
			return tr("5-hour quota")
		}
		if m == 10080 {
			return tr("Weekly quota")
		}
		if m > 0 && m%1440 == 0 {
			return fmt.Sprintf(tr("%d-day quota"), m/1440)
		}
		if m > 0 && m%60 == 0 {
			return fmt.Sprintf(tr("%d-hour quota"), m/60)
		}
		if m > 0 {
			return fmt.Sprintf(tr("%d-minute quota"), m)
		}
	}
	if index == 0 {
		return tr("Primary quota")
	}
	return tr("Secondary quota")
}
