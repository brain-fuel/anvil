// Package schedule finds meeting slots across many people, each of whom may
// carry busy time merged from any number of calendars.
package schedule

import (
	"errors"
	"sort"
	"time"

	"goforge.dev/anvil/interval"
)

// Person is an attendee. Busy is the union of every calendar they live by —
// personal, work, volunteer — merged into one normalized set.
type Person struct {
	Name string
	Busy interval.Set
}

// Merge folds any number of busy sets (one per calendar) into a single
// Person.
func Merge(name string, calendars ...interval.Set) Person {
	var busy interval.Set
	for _, c := range calendars {
		busy = busy.Union(c)
	}
	return Person{Name: name, Busy: busy}
}

// Request describes the meeting being scheduled.
type Request struct {
	Duration time.Duration  // meeting length (required)
	Window   interval.Span  // search window (required)
	Location *time.Location // wall-clock zone for Hours/Days; default UTC
	HourFrom time.Duration  // earliest start within a day, e.g. 9h; default 9h
	HourTo   time.Duration  // latest end within a day, e.g. 17h; default 17h
	Days     []time.Weekday // allowed days; default Mon–Fri
	Step     time.Duration  // candidate granularity; default 30m
	Travel   time.Duration  // drive-time padding before and after, for in-person meetings
	Required []Person       // everyone here must be free
	Optional []Person       // scored: more of these free is better
	Limit    int            // max slots returned; default 10
}

// Slot is one candidate meeting time.
type Slot struct {
	interval.Span
	Attending []string // optional attendees who can make it (required are implied)
	Missing   []string // optional attendees who cannot
}

// Score ranks a slot: every optional attendee present counts.
func (s Slot) Score() int { return len(s.Attending) }

var (
	ErrNoDuration = errors.New("schedule: request needs a positive Duration")
	ErrNoWindow   = errors.New("schedule: request needs a non-empty Window")
)

// Find returns up to Limit candidate slots, best first: most optional
// attendees, then earliest. All required attendees fit every returned slot,
// including Travel padding on both sides.
func Find(req Request) ([]Slot, error) {
	if req.Duration <= 0 {
		return nil, ErrNoDuration
	}
	if req.Window.IsEmpty() {
		return nil, ErrNoWindow
	}
	loc := req.Location
	if loc == nil {
		loc = time.UTC
	}
	hourFrom, hourTo := req.HourFrom, req.HourTo
	if hourFrom == 0 && hourTo == 0 {
		hourFrom, hourTo = 9*time.Hour, 17*time.Hour
	}
	days := req.Days
	if len(days) == 0 {
		days = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	}
	step := req.Step
	if step <= 0 {
		step = 30 * time.Minute
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	mask := hoursMask(req.Window, loc, hourFrom, hourTo, days)

	// A slot works for a person when [start-Travel, end+Travel) avoids their
	// busy time. Free-of-busy shrunk by Travel = times where the padded slot
	// still fits.
	fit := func(p Person) interval.Set {
		return p.Busy.Complement(req.Window.Pad(req.Travel, req.Travel)).
			Shrink(req.Travel, req.Travel)
	}

	open := mask
	for _, p := range req.Required {
		open = open.Intersect(fit(p))
	}

	optFree := make([]interval.Set, len(req.Optional))
	for i, p := range req.Optional {
		optFree[i] = fit(p)
	}

	var slots []Slot
	for _, span := range open {
		for t := alignUp(span.Start, step, loc); !t.Add(req.Duration).After(span.End); t = t.Add(step) {
			s := Slot{Span: interval.Span{Start: t, End: t.Add(req.Duration)}}
			for i, p := range req.Optional {
				if optFree[i].ContainsSpan(s.Span) {
					s.Attending = append(s.Attending, p.Name)
				} else {
					s.Missing = append(s.Missing, p.Name)
				}
			}
			slots = append(slots, s)
		}
	}

	sort.SliceStable(slots, func(i, j int) bool {
		if slots[i].Score() != slots[j].Score() {
			return slots[i].Score() > slots[j].Score()
		}
		return slots[i].Start.Before(slots[j].Start)
	})
	if len(slots) > limit {
		slots = slots[:limit]
	}
	return slots, nil
}

// hoursMask builds the allowed-time set: the given wall-clock hours on the
// given weekdays, in loc, clipped to the window.
func hoursMask(window interval.Span, loc *time.Location, from, to time.Duration, days []time.Weekday) interval.Set {
	allowed := make(map[time.Weekday]bool, len(days))
	for _, d := range days {
		allowed[d] = true
	}
	var spans []interval.Span
	day := time.Date(window.Start.In(loc).Year(), window.Start.In(loc).Month(), window.Start.In(loc).Day(), 0, 0, 0, 0, loc)
	for day.Before(window.End) {
		if allowed[day.Weekday()] {
			spans = append(spans, interval.Span{Start: day.Add(from), End: day.Add(to)})
		}
		day = day.AddDate(0, 0, 1)
	}
	return interval.Normalize(spans).Intersect(interval.Set{window})
}

// alignUp rounds t up to the next multiple of step on the wall clock in loc.
func alignUp(t time.Time, step time.Duration, loc *time.Location) time.Time {
	lt := t.In(loc)
	midnight := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	off := lt.Sub(midnight)
	if rem := off % step; rem != 0 {
		return midnight.Add(off - rem + step)
	}
	return lt
}
