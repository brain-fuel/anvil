package serve

import (
	"sync"
	"time"

	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
)

// Demo builds a fully working server with synthetic calendars — no config
// file, no providers, no credentials. Booking writes into an in-memory
// calendar that immediately blocks the slot, so the whole loop is visible:
// busy merging, slot search, booking, agenda.
func Demo(listen string) (*Server, error) {
	if listen == "" {
		listen = ":8080"
	}
	loc := time.Local
	now := time.Now().In(loc)
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	at := func(d, h, m int) time.Time {
		return day0.AddDate(0, 0, d).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}
	ev := func(summary string, start, end time.Time) ics.Event {
		return ics.Event{UID: ics.NewUID(), Summary: summary, Start: start, End: end}
	}

	var mikeWork, mikePersonal, melissa []ics.Event
	for d := 0; d < 8; d++ {
		wd := day0.AddDate(0, 0, d).Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			continue
		}
		mikeWork = append(mikeWork,
			ev("Standup", at(d, 9, 30), at(d, 10, 0)),
			ev("Lunch", at(d, 12, 0), at(d, 12, 30)))
		switch wd {
		case time.Monday, time.Wednesday:
			melissa = append(melissa, ev("1:1s", at(d, 10, 0), at(d, 11, 30)))
		case time.Tuesday, time.Thursday:
			mikePersonal = append(mikePersonal, ev("School pickup", at(d, 15, 0), at(d, 16, 0)))
		case time.Friday:
			melissa = append(melissa, ics.Event{
				UID: ics.NewUID(), Summary: "Coffee with Stan",
				Location: "Blue Bottle, 300 Webster St, Oakland",
				Start:    at(d, 9, 0), End: at(d, 10, 0),
			})
		}
	}
	mikeWork = append(mikeWork, ics.Event{
		UID: ics.NewUID(), Summary: "Design review",
		Description: "Join: https://meet.jit.si/anvil-demo-review",
		Start:       at(1, 14, 0), End: at(1, 15, 30),
	})

	mem := func(name string, events []ics.Event) *memCalendar {
		return &memCalendar{cal: &ics.Calendar{Name: name, Events: events}}
	}
	booked := mem("booked", nil)
	booked.join = "https://meet.jit.si/anvil-demo"

	cfg := &Config{
		Listen:   listen,
		Timezone: loc.String(),
		Agenda:   []string{"mike-work", "mike-personal", "melissa", "booked"},
		People: []PersonConfig{
			{Name: "mike", Email: "mike@example.com", Calendars: []string{"mike-work", "mike-personal", "booked"}},
			{Name: "melissa", Email: "melissa@example.com", Calendars: []string{"melissa", "booked"}},
		},
		Links: []LinkConfig{{
			Slug: "intro", Title: "Intro with Mike & Melissa (demo)",
			DurationM: 30, Required: []string{"mike", "melissa"},
			DaysAhead: 7, MinNoticeH: 1,
			VideoURL: "https://meet.jit.si/anvil-demo",
			BookInto: "booked",
		}},
	}
	s := &Server{
		cfg: cfg,
		loc: loc,
		sources: map[string]Source{
			"mike-work":     mem("mike-work", mikeWork),
			"mike-personal": mem("mike-personal", mikePersonal),
			"melissa":       mem("melissa", melissa),
			"booked":        booked,
		},
		writers: map[string]Writer{"booked": booked},
		people:  map[string]PersonConfig{},
		now:     time.Now,
	}
	for _, p := range cfg.People {
		s.people[p.Name] = p
	}
	return s, nil
}

// memCalendar is an in-memory Source and Writer: bookings land here and
// immediately count as busy.
type memCalendar struct {
	mu   sync.Mutex
	cal  *ics.Calendar
	join string
}

func (m *memCalendar) Events(interval.Span) (*ics.Calendar, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := &ics.Calendar{Name: m.cal.Name, Events: append([]ics.Event{}, m.cal.Events...)}
	return cp, nil
}

func (m *memCalendar) Busy(window interval.Span) (interval.Set, error) {
	cal, err := m.Events(window)
	if err != nil {
		return nil, err
	}
	return cal.Busy(window), nil
}

func (m *memCalendar) CreateEvent(ev ics.Event, meet bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cal.Events = append(m.cal.Events, ev)
	return m.join, nil
}
