package schedule

import (
	"testing"
	"time"

	"goforge.dev/anvil/interval"
)

var ny = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return l
}()

// Wed 2026-06-10 through Fri 2026-06-12, New York.
func at(day, h, m int) time.Time {
	return time.Date(2026, 6, day, h, m, 0, 0, ny)
}
func sp(day, h1, m1, h2, m2 int) interval.Span {
	return interval.Span{Start: at(day, h1, m1), End: at(day, h2, m2)}
}

func baseRequest() Request {
	return Request{
		Duration: time.Hour,
		Window:   interval.Span{Start: at(10, 0, 0), End: at(13, 0, 0)},
		Location: ny,
	}
}

func TestFindRespectsAllRequiredCalendars(t *testing.T) {
	mike := Merge("mike",
		interval.Normalize([]interval.Span{sp(10, 9, 0, 12, 0)}),  // work: Wed morning blocked
		interval.Normalize([]interval.Span{sp(10, 13, 0, 17, 0)}), // personal: Wed afternoon blocked
	)
	melissa := Merge("melissa",
		interval.Normalize([]interval.Span{sp(11, 9, 0, 17, 0)}), // all Thursday blocked
	)
	req := baseRequest()
	req.Required = []Person{mike, melissa}

	slots, err := Find(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
	// Only opening: Wed 12:00–13:00 (mike) doesn't exist for melissa? melissa
	// is free Wednesday — so Wed 12:00 works; Friday is wide open.
	first := slots[0]
	if !first.Start.Equal(at(10, 12, 0)) {
		t.Fatalf("first slot %v, want Wed 12:00", first.Start)
	}
	for _, s := range slots {
		for _, p := range req.Required {
			if !p.Busy.Complement(req.Window).ContainsSpan(s.Span) {
				t.Fatalf("slot %v conflicts with %s", s.Span, p.Name)
			}
		}
	}
}

func TestFindRanksByOptionalAttendance(t *testing.T) {
	stan := Person{Name: "stan", Busy: interval.Normalize([]interval.Span{sp(12, 9, 0, 12, 0)})}
	david := Person{Name: "david", Busy: interval.Normalize([]interval.Span{sp(12, 9, 0, 10, 0)})}
	req := baseRequest()
	req.Window = interval.Span{Start: at(12, 0, 0), End: at(13, 0, 0)} // Friday only
	req.Optional = []Person{stan, david}

	slots, err := Find(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
	best := slots[0]
	if best.Score() != 2 {
		t.Fatalf("best slot %v has score %d, want 2 (both optional)", best.Span, best.Score())
	}
	if best.Start.Before(at(12, 12, 0)) {
		t.Fatalf("best slot %v starts before stan is free", best.Start)
	}
}

func TestFindAddsTravelPadding(t *testing.T) {
	// Busy 9–10 and 12–17; meeting 1h with 30m travel each side.
	// Padded fit needs [start-30m, end+30m) clear: only 10:30–11:30 works.
	p := Person{Name: "mike", Busy: interval.Normalize([]interval.Span{
		sp(10, 9, 0, 10, 0), sp(10, 12, 0, 17, 0),
	})}
	req := baseRequest()
	req.Window = interval.Span{Start: at(10, 0, 0), End: at(11, 0, 0)} // Wednesday
	req.Travel = 30 * time.Minute
	req.Required = []Person{p}

	slots, err := Find(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 {
		t.Fatalf("got %d slots %v, want exactly 1", len(slots), slots)
	}
	if !slots[0].Start.Equal(at(10, 10, 30)) || !slots[0].End.Equal(at(10, 11, 30)) {
		t.Fatalf("got %v, want Wed 10:30–11:30", slots[0].Span)
	}
}

func TestFindSkipsWeekends(t *testing.T) {
	req := baseRequest()
	// Sat 2026-06-13 through Sun 2026-06-14.
	req.Window = interval.Span{Start: at(13, 0, 0), End: at(15, 0, 0)}
	slots, err := Find(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Fatalf("got %d slots on a weekend, want 0", len(slots))
	}
}

func TestFindValidation(t *testing.T) {
	if _, err := Find(Request{Window: sp(10, 9, 0, 17, 0)}); err != ErrNoDuration {
		t.Fatalf("got %v, want ErrNoDuration", err)
	}
	if _, err := Find(Request{Duration: time.Hour}); err != ErrNoWindow {
		t.Fatalf("got %v, want ErrNoWindow", err)
	}
}
