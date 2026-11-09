package cron

import (
	"testing"
	"time"
)

func TestParseAndNext(t *testing.T) {
	s, err := Parse("0 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	next := s.Next(base)
	want := time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestStepAndRanges(t *testing.T) {
	s, err := Parse("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 9, 8, 59, 0, 0, time.UTC) // Monday
	next := s.Next(base)
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Fatalf("next = %s", next)
	}
	if next.Weekday() != time.Monday {
		t.Fatalf("weekday = %s", next.Weekday())
	}
}

func TestInvalidExpressions(t *testing.T) {
	for _, expr := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *"} {
		if _, err := Parse(expr); err == nil {
			t.Fatalf("accepted %q", expr)
		}
	}
}

func TestDayOfMonthAndWeekdayOR(t *testing.T) {
	s, err := Parse("0 0 1 * 1")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-09 is a Monday; the 1st is a Sunday. The next match should be
	// Monday the 9th because standard cron treats restricted day fields as OR.
	base := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	want := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}
