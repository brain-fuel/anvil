package ics

import (
	"strings"
	"testing"
	"time"

	"goforge.dev/anvil/interval"
)

func TestEncodeRoundTrip(t *testing.T) {
	in := Event{
		UID:         "abc123@anvil",
		Summary:     "Sync; with, specials\nand a newline",
		Location:    "12 Main St, Springfield",
		Description: strings.Repeat("long description ", 20),
		URL:         "https://meet.jit.si/anvil-sync",
		Start:       time.Date(2026, 6, 17, 17, 30, 0, 0, time.UTC),
		End:         time.Date(2026, 6, 17, 18, 15, 0, 0, time.UTC),
		Stamp:       time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		Organizer:   Attendee{Name: "Mike", Email: "mike@example.com"},
		Attendees: []Attendee{
			{Name: "Melissa", Email: "melissa@example.com"},
			{Email: "guest@example.org"},
		},
	}
	var b strings.Builder
	if err := Encode(&b, "REQUEST", []Event{in}); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 76 { // 75 + leading fold space
			t.Errorf("line over 75 octets: %q", line)
		}
	}
	if !strings.Contains(out, "METHOD:REQUEST") {
		t.Error("missing METHOD")
	}

	cal, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Events) != 1 {
		t.Fatalf("got %d events", len(cal.Events))
	}
	got := cal.Events[0]
	if got.Summary != in.Summary {
		t.Errorf("summary %q != %q", got.Summary, in.Summary)
	}
	if got.Location != in.Location {
		t.Errorf("location %q", got.Location)
	}
	if got.Description != in.Description {
		t.Errorf("description mismatch")
	}
	if !got.Start.Equal(in.Start) || !got.End.Equal(in.End) {
		t.Errorf("times %v–%v", got.Start, got.End)
	}
	if got.UID != in.UID {
		t.Errorf("uid %q", got.UID)
	}
	if got.Organizer.Email != "mike@example.com" || got.Organizer.Name != "Mike" {
		t.Errorf("organizer %+v", got.Organizer)
	}
	if len(got.Attendees) != 2 || got.Attendees[0].Email != "melissa@example.com" ||
		got.Attendees[0].Name != "Melissa" || got.Attendees[1].Email != "guest@example.org" {
		t.Errorf("attendees %+v", got.Attendees)
	}
}

func TestEncodeAllDay(t *testing.T) {
	ev := Event{
		UID:    "d1@anvil",
		AllDay: true,
		Start:  time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
	}
	var b strings.Builder
	if err := Encode(&b, "", []Event{ev}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "DTSTART;VALUE=DATE:20260617") {
		t.Errorf("output: %s", b.String())
	}
	if strings.Contains(b.String(), "METHOD") {
		t.Error("METHOD should be omitted")
	}
}

func TestEncodeRequiresUID(t *testing.T) {
	var b strings.Builder
	err := Encode(&b, "", []Event{{Start: time.Now(), End: time.Now()}})
	if err == nil {
		t.Fatal("expected error for missing UID")
	}
}

func TestNewUIDUnique(t *testing.T) {
	a, c := NewUID(), NewUID()
	if a == c || !strings.HasSuffix(a, "@anvil") {
		t.Fatalf("%q %q", a, c)
	}
}

func span(a, b time.Time) interval.Span { return interval.Span{Start: a, End: b} }

func TestOccurrencesIn(t *testing.T) {
	cal, err := ParseIn(strings.NewReader(sample), ny)
	if err != nil {
		t.Fatal(err)
	}
	window := span(time.Date(2026, 6, 8, 0, 0, 0, 0, ny), time.Date(2026, 6, 13, 0, 0, 0, 0, ny))
	occ := cal.OccurrencesIn(window)
	// Standup Wed Jun 10 (Fri 12 EXDATEd), offsite Jun 11 all day, focus
	// block Jun 10 (transparent — agenda still shows it). Cancelled excluded.
	if len(occ) != 3 {
		t.Fatalf("got %d occurrences: %+v", len(occ), occ)
	}
	if occ[0].Event.Summary != "Standup with a very long folded summary line" {
		t.Errorf("first %q", occ[0].Event.Summary)
	}
	if occ[1].Event.Summary != "Focus block" {
		t.Errorf("second %q", occ[1].Event.Summary)
	}
	if occ[2].Event.Summary != "Offsite" {
		t.Errorf("third %q", occ[2].Event.Summary)
	}
}
