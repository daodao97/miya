package automation

import (
	"fmt"
	"time"
)

// WeekdayNames are the short day names times are written with.
var WeekdayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// FormatTime writes a run or schedule time as the automation pages show it.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return "Not scheduled"
	}
	return fmt.Sprintf("%d/%d (%s) %s", t.Month(), t.Day(), WeekdayNames[t.Weekday()], t.Format("15:04"))
}
