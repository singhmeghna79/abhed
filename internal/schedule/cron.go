// Package schedule runs prompts on a timetable.
//
// A recurring run is the shape of a lot of real work — "every weekday at nine,
// summarise what changed overnight", "hourly, check the staging pods" — and
// until now the only way to get it was an external cron calling `titan -p`.
// That works, but the run then lives outside the console: no session in the
// list, no audit trail beside the interactive ones, no admin view of what is
// due. This puts the timetable inside the server, so a scheduled run is an
// ordinary session that happened to be started by the clock.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Expr is a parsed five-field cron expression: minute hour day-of-month
// month day-of-week. Supports *, lists, ranges, steps, and the usual
// @hourly/@daily/@weekly/@monthly aliases. Nothing more, deliberately: the
// point is to be readable by whoever has to debug why a job ran at 3am.
type Expr struct {
	src    string
	minute [60]bool
	hour   [24]bool
	dom    [32]bool // 1..31
	month  [13]bool // 1..12
	dow    [7]bool  // 0..6, Sunday = 0
	// domAny/dowAny record whether the field was "*", which changes how the
	// two day fields combine: cron treats a wildcard day field as unrestricted
	// and a specified one as an OR with the other.
	domAny, dowAny bool
}

var aliases = map[string]string{
	"@hourly":  "0 * * * *",
	"@daily":   "0 0 * * *",
	"@weekly":  "0 0 * * 0",
	"@monthly": "0 0 1 * *",
}

// Parse compiles an expression.
func Parse(s string) (*Expr, error) {
	src := strings.TrimSpace(s)
	if a, ok := aliases[strings.ToLower(src)]; ok {
		src = a
	}
	fields := strings.Fields(src)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron %q: want 5 fields (minute hour day month weekday), got %d", s, len(fields))
	}
	e := &Expr{src: s}
	var err error
	if err = fill(e.minute[:], fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("cron %q minute: %w", s, err)
	}
	if err = fill(e.hour[:], fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("cron %q hour: %w", s, err)
	}
	if err = fill(e.dom[:], fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("cron %q day: %w", s, err)
	}
	if err = fill(e.month[:], fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("cron %q month: %w", s, err)
	}
	if err = fill(e.dow[:], fields[4], 0, 6); err != nil {
		return nil, fmt.Errorf("cron %q weekday: %w", s, err)
	}
	e.domAny = fields[2] == "*"
	e.dowAny = fields[4] == "*"
	return e, nil
}

func fill(set []bool, field string, lo, hi int) error {
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fmt.Errorf("bad step in %q", part)
			}
			step, part = n, part[:i]
		}
		start, end := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			var err1, err2 error
			start, err1 = strconv.Atoi(a)
			end, err2 = strconv.Atoi(b)
			if err1 != nil || err2 != nil {
				return fmt.Errorf("bad range %q", part)
			}
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("bad value %q", part)
			}
			start, end = n, n
			if step > 1 { // "5/15" means from 5 to the end, every 15
				end = hi
			}
		}
		if start < lo || end > hi || start > end {
			return fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		// Weekday 7 is Sunday in some crons; accept it.
		for v := start; v <= end; v += step {
			set[v] = true
		}
	}
	return nil
}

// Matches reports whether t is a firing minute.
func (e *Expr) Matches(t time.Time) bool {
	if !e.minute[t.Minute()] || !e.hour[t.Hour()] || !e.month[int(t.Month())] {
		return false
	}
	dom := e.dom[t.Day()]
	dow := e.dow[int(t.Weekday())]
	switch {
	case e.domAny && e.dowAny:
		return true
	case e.domAny:
		return dow
	case e.dowAny:
		return dom
	default:
		return dom || dow
	}
}

// Next returns the first firing minute strictly after t, or the zero time if
// none falls within the next four years — which for a valid expression means
// a day/month combination that never exists, like 31 February.
func (e *Expr) Next(t time.Time) time.Time {
	c := t.Truncate(time.Minute).Add(time.Minute)
	limit := c.AddDate(4, 0, 0)
	for c.Before(limit) {
		if !e.month[int(c.Month())] {
			// Skip to the first of next month.
			c = time.Date(c.Year(), c.Month()+1, 1, 0, 0, 0, 0, c.Location())
			continue
		}
		if !e.Matches(c) {
			if !e.hour[c.Hour()] {
				c = c.Truncate(time.Hour).Add(time.Hour)
				continue
			}
			c = c.Add(time.Minute)
			continue
		}
		return c
	}
	return time.Time{}
}

func (e *Expr) String() string { return e.src }
