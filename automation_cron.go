package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSchedule is a standard five-field cron expression, in local time:
// minute, hour, day of month, month, day of week. Each field is a set of
// allowed values as bits.
type cronSchedule struct {
	minute, hour, dom, month, dow uint64
	// A field written * places no limit; when day of month and day of week
	// are both limited, a day matching either is due, as cron has it.
	domAny, dowAny bool
}

var cronNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

func parseCron(spec string) (cronSchedule, error) {
	f := strings.Fields(spec)
	if len(f) != 5 {
		return cronSchedule{}, errors.New(tr("Expected 5 fields: minute hour day month weekday"))
	}
	limits := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	var bits [5]uint64
	for i, field := range f {
		b, err := parseCronField(strings.ToLower(field), limits[i][0], limits[i][1])
		if err != nil {
			return cronSchedule{}, fmt.Errorf(tr("Field %d %q: %v"), i+1, field, err)
		}
		bits[i] = b
	}
	return cronSchedule{minute: bits[0], hour: bits[1], dom: bits[2], month: bits[3], dow: sundayAsZero(bits[4]),
		domAny: cronAny(f[2]), dowAny: cronAny(f[4])}, nil
}

func cronAny(field string) bool { return field == "*" || field == "?" }

// sundayAsZero folds day 7, another name for Sunday, into day 0.
func sundayAsZero(b uint64) uint64 {
	if b&(1<<7) != 0 {
		b = b&^(1<<7) | 1
	}
	return b
}

func parseCronField(s string, lo, hi int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(s, ",") {
		step := 1
		if base, st, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(st)
			if err != nil || n < 1 {
				return 0, errors.New(tr("Invalid step"))
			}
			step, part = n, base
		}
		a, b := lo, hi
		if !cronAny(part) {
			x, y, isRange := strings.Cut(part, "-")
			var err error
			if a, err = cronValue(x, lo, hi); err != nil {
				return 0, err
			}
			b = a
			switch {
			case isRange:
				if b, err = cronValue(y, lo, hi); err != nil {
					return 0, err
				}
				if b < a {
					return 0, errors.New(tr("Range start exceeds range end"))
				}
			case step > 1:
				b = hi
			}
		}
		for v := a; v <= b; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

func cronValue(s string, lo, hi int) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		n, ok := cronNames[s]
		if !ok {
			return 0, fmt.Errorf(tr("Unrecognized value %q"), s)
		}
		v = n
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf(tr("%d is outside the range %d–%d"), v, lo, hi)
	}
	return v, nil
}

// Next is the first due minute after after, or zero when none comes within
// five years.
func (s cronSchedule) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	loc := t.Location()
	limit := after.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case s.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !s.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case s.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
		case s.minute&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

func (s cronSchedule) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<uint(t.Day())) != 0
	dow := s.dow&(1<<uint(t.Weekday())) != 0
	if s.domAny || s.dowAny {
		return dom && dow
	}
	return dom || dow
}

// describeCron says a schedule in Chinese, as the list and the cards show
// it; a schedule it has no words for is shown as written.
func describeCron(spec string) string {
	f := strings.Fields(spec)
	if _, err := parseCron(spec); err != nil {
		return spec
	}
	minute, hour, dom, month, dow := f[0], f[1], f[2], f[3], f[4]
	if !cronAny(month) {
		return spec
	}
	if strings.HasPrefix(minute, "*/") && cronAny(hour) && cronAny(dom) && cronAny(dow) {
		return tr("Every ") + minute[2:] + tr(" minutes")
	}
	m, err := strconv.Atoi(minute)
	if err != nil {
		return spec
	}
	if cronAny(hour) && cronAny(dom) && cronAny(dow) {
		if m == 0 {
			return tr("Hourly")
		}
		return fmt.Sprintf(tr("Every hour at minute %d"), m)
	}
	if strings.HasPrefix(hour, "*/") && cronAny(dom) && cronAny(dow) {
		return tr("Every ") + hour[2:] + tr(" hours")
	}
	h, err := strconv.Atoi(hour)
	if err != nil {
		return spec
	}
	at := fmt.Sprintf("%d:%02d", h, m)
	switch {
	case cronAny(dom) && cronAny(dow):
		return tr("Daily at ") + at
	case cronAny(dom):
		if days := describeCronDays(dow); days != "" {
			return days + " " + at
		}
	case cronAny(dow):
		if n, err := strconv.Atoi(dom); err == nil {
			return fmt.Sprintf(tr("Monthly on day %d at %s"), n, at)
		}
	}
	return spec
}

func describeCronDays(dow string) string {
	b, err := parseCronField(strings.ToLower(dow), 0, 7)
	if err != nil {
		return ""
	}
	switch b = sundayAsZero(b); b {
	case 0b0111110:
		return tr("Weekdays")
	case 0b1000001:
		return tr("Weekends")
	}
	names := []string{tr("Sun"), tr("Mon"), tr("Tue"), tr("Wed"), tr("Thu"), tr("Fri"), tr("Sat")}
	var parts []string
	for d := 1; d <= 7; d++ {
		if i := d % 7; b&(1<<uint(i)) != 0 {
			parts = append(parts, names[i])
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return tr("Weekly: ") + strings.Join(parts, ", ")
}
