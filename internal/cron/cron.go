// Package cron implements the small subset of standard five-field cron needed
// for backup schedules: *, lists, ranges, and steps. It intentionally has no
// external dependency.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression in UTC.
type Schedule struct {
	Minute, Hour, Day, Month, Weekday map[int]bool
	Expr                              string
}

// Parse parses a five-field expression.
func Parse(expr string) (*Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields")
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	sets := make([]map[int]bool, 5)
	for i, f := range fields {
		set, err := parseField(f, ranges[i][0], ranges[i][1])
		if err != nil {
			return nil, fmt.Errorf("cron field %d: %w", i+1, err)
		}
		sets[i] = set
	}
	if sets[4][7] {
		sets[4][0] = true
	}
	return &Schedule{Minute: sets[0], Hour: sets[1], Day: sets[2], Month: sets[3], Weekday: sets[4], Expr: strings.Join(fields, " ")}, nil
}

func parseField(field string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, fmt.Errorf("empty list item")
		}
		step := 1
		rangePart := part
		if idx := strings.Index(part, "/"); idx >= 0 {
			rangePart = part[:idx]
			var err error
			step, err = strconv.Atoi(part[idx+1:])
			if err != nil || step < 1 {
				return nil, fmt.Errorf("invalid step %q", part)
			}
		}
		lo, hi := min, max
		if rangePart != "*" {
			if idx := strings.Index(rangePart, "-"); idx >= 0 {
				var err error
				lo, err = strconv.Atoi(rangePart[:idx])
				if err != nil {
					return nil, fmt.Errorf("invalid range %q", part)
				}
				hi, err = strconv.Atoi(rangePart[idx+1:])
				if err != nil {
					return nil, fmt.Errorf("invalid range %q", part)
				}
			} else {
				v, err := strconv.Atoi(rangePart)
				if err != nil {
					return nil, fmt.Errorf("invalid value %q", rangePart)
				}
				lo, hi = v, v
			}
		}
		if lo < min || hi > max || lo > hi {
			return nil, fmt.Errorf("value outside %d-%d", min, max)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty set")
	}
	return out, nil
}

// Next returns the first matching time strictly after t, in t's location.
func (s *Schedule) Next(t time.Time) time.Time {
	if s == nil {
		return time.Time{}
	}
	loc := t.Location()
	start := t.Truncate(time.Minute).Add(time.Minute)
	// Search up to four years to accommodate leap-day expressions.
	limit := start.AddDate(4, 0, 0)
	domAll := len(s.Day) == 31
	dowAll := len(s.Weekday) == 7
	for cur := start; cur.Before(limit); cur = cur.Add(time.Minute) {
		// Skip quickly by day when the date does not match.
		if !s.Month[int(cur.Month())] {
			nextMonth := time.Date(cur.Year(), cur.Month()+1, 1, 0, 0, 0, 0, loc)
			cur = nextMonth.Add(-time.Minute)
			continue
		}
		dayMatch := s.Day[cur.Day()]
		weekdayMatch := s.Weekday[int(cur.Weekday())]
		dateOK := true
		switch {
		case domAll && dowAll:
		case domAll:
			dateOK = weekdayMatch
		case dowAll:
			dateOK = dayMatch
		default:
			dateOK = dayMatch || weekdayMatch
		}
		if !dateOK {
			nextDay := time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, loc)
			cur = nextDay.Add(-time.Minute)
			continue
		}
		if !s.Hour[cur.Hour()] {
			cur = time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour()+1, 0, 0, 0, loc).Add(-time.Minute)
			continue
		}
		if !s.Minute[cur.Minute()] {
			continue
		}
		return cur
	}
	return time.Time{}
}

// NextFromString parses and computes the next run.
func NextFromString(expr string, from time.Time) (time.Time, error) {
	s, err := Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	next := s.Next(from)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("cron expression has no future run")
	}
	return next, nil
}

// Validate checks an expression.
func Validate(expr string) error {
	_, err := Parse(expr)
	return err
}
