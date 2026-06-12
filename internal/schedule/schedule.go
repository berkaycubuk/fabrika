// Package schedule evaluates standard 5-field cron expressions
// (minute hour day-of-month month day-of-week) using only the stdlib.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// field describes the valid range of a single cron field.
type field struct {
	min, max int
}

var fields = [5]field{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day-of-month
	{1, 12}, // month
	{0, 7},  // day-of-week (0 and 7 are both Sunday)
}

// parsed holds the set of allowed values for each cron field.
type parsed struct {
	minute, hour, dom, month, dow map[int]bool
	domRestricted, dowRestricted  bool
}

// parse validates and decomposes a 5-field cron expression.
func parse(expr string) (*parsed, error) {
	parts := strings.Fields(strings.TrimSpace(expr))
	if len(parts) != 5 {
		return nil, fmt.Errorf("schedule: expected 5 fields, got %d", len(parts))
	}

	sets := make([]map[int]bool, 5)
	for i, part := range parts {
		set, err := parseField(part, fields[i])
		if err != nil {
			return nil, fmt.Errorf("schedule: field %d (%q): %w", i+1, part, err)
		}
		sets[i] = set
	}

	p := &parsed{
		minute:        sets[0],
		hour:          sets[1],
		dom:           sets[2],
		month:         sets[3],
		dow:           normalizeDOW(sets[4]),
		domRestricted: parts[2] != "*",
		dowRestricted: parts[4] != "*",
	}
	return p, nil
}

// normalizeDOW maps day-of-week 7 to 0 so both represent Sunday.
func normalizeDOW(set map[int]bool) map[int]bool {
	if set[7] {
		set[0] = true
		delete(set, 7)
	}
	return set
}

// parseField parses a single cron field into the set of values it matches.
func parseField(s string, f field) (map[int]bool, error) {
	set := make(map[int]bool)
	for token := range strings.SplitSeq(s, ",") {
		if token == "" {
			return nil, fmt.Errorf("empty token")
		}

		// Split off an optional step.
		rangePart := token
		step := 1
		if idx := strings.Index(token, "/"); idx >= 0 {
			rangePart = token[:idx]
			stepStr := token[idx+1:]
			n, err := strconv.Atoi(stepStr)
			if err != nil {
				return nil, fmt.Errorf("invalid step %q", stepStr)
			}
			if n <= 0 {
				return nil, fmt.Errorf("step must be positive, got %d", n)
			}
			step = n
		}

		var lo, hi int
		switch {
		case rangePart == "*":
			lo, hi = f.min, f.max
		case strings.Contains(rangePart, "-"):
			bounds := strings.SplitN(rangePart, "-", 2)
			a, err := strconv.Atoi(bounds[0])
			if err != nil {
				return nil, fmt.Errorf("invalid range start %q", bounds[0])
			}
			b, err := strconv.Atoi(bounds[1])
			if err != nil {
				return nil, fmt.Errorf("invalid range end %q", bounds[1])
			}
			lo, hi = a, b
		default:
			n, err := strconv.Atoi(rangePart)
			if err != nil {
				return nil, fmt.Errorf("invalid value %q", rangePart)
			}
			lo, hi = n, n
		}

		if lo > hi {
			return nil, fmt.Errorf("range start %d greater than end %d", lo, hi)
		}
		if lo < f.min || hi > f.max {
			return nil, fmt.Errorf("value out of range [%d-%d]", f.min, f.max)
		}

		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	return set, nil
}

// Valid reports whether expr is a well-formed 5-field cron expression.
func Valid(expr string) error {
	_, err := parse(expr)
	return err
}

// matches reports whether t satisfies the parsed expression.
func (p *parsed) matches(t time.Time) bool {
	if !p.minute[t.Minute()] || !p.hour[t.Hour()] || !p.month[int(t.Month())] {
		return false
	}

	dom := p.dom[t.Day()]
	dow := p.dow[int(t.Weekday())] // time.Weekday: Sunday=0..Saturday=6

	switch {
	case p.domRestricted && p.dowRestricted:
		return dom || dow
	case p.domRestricted:
		return dom
	case p.dowRestricted:
		return dow
	default:
		return true
	}
}

// Next returns the earliest time strictly after `after` (truncated to the
// minute) that satisfies expr.
func Next(expr string, after time.Time) (time.Time, error) {
	p, err := parse(expr)
	if err != nil {
		return time.Time{}, err
	}

	// Start at the next whole minute after `after`.
	t := after.Truncate(time.Minute).Add(time.Minute)

	// Cap the search at 5 years of minutes.
	const limit = 5 * 366 * 24 * 60
	for range limit {
		if p.matches(t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("schedule: no match found for %q within search window", expr)
}

// IsDue reports whether a schedule should fire at now.
func IsDue(nextRunAt string, enabled bool, now time.Time) bool {
	if !enabled {
		return false
	}
	if nextRunAt == "" {
		return true
	}
	parsed, err := time.Parse(time.RFC3339, nextRunAt)
	if err != nil {
		return false
	}
	return !parsed.After(now)
}
