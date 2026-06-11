package ics

import (
	"strings"
	"testing"
	"time"

	"goforge.dev/anvil/interval"
)

const sample = `BEGIN:VCALENDAR
VERSION:2.0
X-WR-CALNAME:Work
BEGIN:VEVENT
SUMMARY:Standup with a very long
  folded summary line
DTSTART;TZID=America/New_York:20260610T093000
DTEND;TZID=America/New_York:20260610T100000
RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR;UNTIL=20260701T000000Z
EXDATE;TZID=America/New_York:20260612T093000
END:VEVENT
BEGIN:VEVENT
SUMMARY:Offsite
DTSTART;VALUE=DATE:20260611
DTEND;VALUE=DATE:20260612
END:VEVENT
BEGIN:VEVENT
SUMMARY:Focus block
DTSTART;TZID=America/New_York:20260610T130000
DURATION:PT2H30M
TRANSP:TRANSPARENT
END:VEVENT
BEGIN:VEVENT
SUMMARY:Cancelled thing
DTSTART;TZID=America/New_York:20260610T150000
DTEND;TZID=America/New_York:20260610T160000
STATUS:CANCELLED
END:VEVENT
END:VCALENDAR
`

var ny = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return l
}()

func TestParse(t *testing.T) {
	cal, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if cal.Name != "Work" {
		t.Errorf("name %q", cal.Name)
	}
	if len(cal.Events) != 4 {
		t.Fatalf("got %d events, want 4", len(cal.Events))
	}
	ev := cal.Events[0]
	if ev.Summary != "Standup with a very long folded summary line" {
		t.Errorf("folded summary %q", ev.Summary)
	}
	if !ev.Start.Equal(time.Date(2026, 6, 10, 9, 30, 0, 0, ny)) {
		t.Errorf("start %v", ev.Start)
	}
	if ev.Recur == nil || ev.Recur.Freq != "WEEKLY" || len(ev.Recur.ByDay) != 3 {
		t.Errorf("rrule %+v", ev.Recur)
	}
	if !cal.Events[1].AllDay {
		t.Error("offsite should be all-day")
	}
	if got := cal.Events[2].End.Sub(cal.Events[2].Start); got != 2*time.Hour+30*time.Minute {
		t.Errorf("duration end: %v", got)
	}
}

func TestBusyExpandsRecurrenceWithExdate(t *testing.T) {
	cal, err := ParseIn(strings.NewReader(sample), ny)
	if err != nil {
		t.Fatal(err)
	}
	window := interval.Span{
		Start: time.Date(2026, 6, 8, 0, 0, 0, 0, ny),
		End:   time.Date(2026, 6, 22, 0, 0, 0, 0, ny),
	}
	busy := cal.Busy(window)

	// Standup: DTSTART Wed Jun 10; BYDAY MO,WE,FR; Fri Jun 12 EXDATEd.
	// Expect standups Jun 10, 15, 17, 19 (Jun 8 precedes DTSTART) plus the
	// all-day offsite Jun 11. Transparent and cancelled events excluded.
	wantStarts := []time.Time{
		time.Date(2026, 6, 10, 9, 30, 0, 0, ny),
		time.Date(2026, 6, 11, 0, 0, 0, 0, ny),
		time.Date(2026, 6, 15, 9, 30, 0, 0, ny),
		time.Date(2026, 6, 17, 9, 30, 0, 0, ny),
		time.Date(2026, 6, 19, 9, 30, 0, 0, ny),
	}
	if len(busy) != len(wantStarts) {
		t.Fatalf("got %d busy spans %v, want %d", len(busy), busy, len(wantStarts))
	}
	for i, w := range wantStarts {
		if !busy[i].Start.Equal(w) {
			t.Errorf("span %d starts %v, want %v", i, busy[i].Start, w)
		}
	}
}

func TestDailyCount(t *testing.T) {
	src := `BEGIN:VEVENT
DTSTART:20260601T120000Z
DTEND:20260601T123000Z
RRULE:FREQ=DAILY;INTERVAL=2;COUNT=3
END:VEVENT`
	cal, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	window := interval.Span{
		Start: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
	busy := cal.Busy(window)
	if len(busy) != 3 {
		t.Fatalf("got %d spans %v, want 3", len(busy), busy)
	}
	if !busy[2].Start.Equal(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("last occurrence %v", busy[2].Start)
	}
}

func TestISODuration(t *testing.T) {
	cases := map[string]time.Duration{
		"PT1H30M": 90 * time.Minute,
		"P1D":     24 * time.Hour,
		"P1W":     7 * 24 * time.Hour,
		"-PT15M":  -15 * time.Minute,
		"P1DT12H": 36 * time.Hour,
	}
	for in, want := range cases {
		got, err := parseISODuration(in)
		if err != nil || got != want {
			t.Errorf("%s: got %v err %v, want %v", in, got, err, want)
		}
	}
	if _, err := parseISODuration("1H"); err == nil {
		t.Error("expected error for bad duration")
	}
}
