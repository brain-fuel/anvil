package agenda

import (
	"strings"
	"testing"
	"time"

	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
)

func mustParse(t *testing.T, src string) *ics.Calendar {
	t.Helper()
	cal, err := ics.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func TestBuildMergesSortsAndLinks(t *testing.T) {
	work := mustParse(t, `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:1
SUMMARY:Zoom sync
DESCRIPTION:Join here: https://us02web.zoom.us/j/123456 see you there.
DTSTART:20260617T140000Z
DTEND:20260617T143000Z
END:VEVENT
BEGIN:VEVENT
UID:2
SUMMARY:Coffee with Stan
LOCATION:Blue Bottle\, 300 Webster St\, Oakland
DTSTART:20260617T100000Z
DTEND:20260617T110000Z
END:VEVENT
END:VCALENDAR`)
	personal := mustParse(t, `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:3
SUMMARY:Standup
LOCATION:https://meet.google.com/abc-defg-hij
DTSTART:20260617T120000Z
DTEND:20260617T121500Z
END:VEVENT
END:VCALENDAR`)

	window := interval.Span{
		Start: time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
	}
	items := Build([]Source{{"work", work}, {"personal", personal}}, window)
	if len(items) != 3 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0].Summary != "Coffee with Stan" || items[1].Summary != "Standup" || items[2].Summary != "Zoom sync" {
		t.Fatalf("order: %v %v %v", items[0].Summary, items[1].Summary, items[2].Summary)
	}
	if items[0].JoinURL != "" || !strings.Contains(items[0].MapsURL, "maps/search") ||
		!strings.Contains(items[0].MapsURL, "Blue+Bottle") {
		t.Errorf("coffee links: %+v", items[0])
	}
	if items[1].JoinURL != "https://meet.google.com/abc-defg-hij" || items[1].MapsURL != "" {
		t.Errorf("standup links: %+v", items[1])
	}
	if items[2].JoinURL != "https://us02web.zoom.us/j/123456" {
		t.Errorf("zoom link: %q", items[2].JoinURL)
	}
	if items[2].Calendar != "work" {
		t.Errorf("calendar tag %q", items[2].Calendar)
	}
}
