// Package ics reads iCalendar (RFC 5545) data into busy time. It is a
// deliberately small reader, not a full implementation: it understands
// VEVENT with DTSTART/DTEND/DURATION, IANA TZIDs (resolved via the Go time
// zone database), all-day events, EXDATE, and DAILY/WEEKLY recurrence with
// INTERVAL, COUNT, UNTIL, and BYDAY. Unsupported recurrence frequencies fall
// back to the first occurrence only. That covers the overwhelming share of
// real calendar feeds — every major provider exports a secret iCal URL.
package ics

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"goforge.dev/anvil/interval"
)

// Event is one VEVENT, recurrence not yet expanded.
type Event struct {
	UID         string
	Summary     string
	Location    string
	Description string
	URL         string
	Start       time.Time
	End         time.Time
	Stamp       time.Time // DTSTAMP; used when encoding
	AllDay      bool
	Transparent bool // TRANSP:TRANSPARENT — shows as free
	Cancelled   bool
	Organizer   Attendee
	Attendees   []Attendee
	Recur       *Recurrence
	ExDates     []time.Time
}

// Attendee is an ORGANIZER or ATTENDEE participant.
type Attendee struct {
	Name  string // CN parameter
	Email string
}

// Recurrence is the supported subset of RRULE.
type Recurrence struct {
	Freq     string // "DAILY" or "WEEKLY"; anything else: first occurrence only
	Interval int
	Count    int
	Until    time.Time
	ByDay    []time.Weekday
}

// Calendar is a parsed feed.
type Calendar struct {
	Name   string
	Events []Event
}

// Parse reads an iCalendar stream. Floating (zone-less) times are
// interpreted in UTC; pass a calendar default via ParseIn if you need
// otherwise.
func Parse(r io.Reader) (*Calendar, error) { return ParseIn(r, time.UTC) }

// ParseIn is Parse with an explicit zone for floating times.
func ParseIn(r io.Reader, floating *time.Location) (*Calendar, error) {
	cal := &Calendar{}
	var ev *Event
	var lineNo int
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var pending string
	flush := func() error {
		if pending == "" {
			return nil
		}
		line := pending
		pending = ""
		name, params, value, ok := splitProperty(line)
		if !ok {
			return nil // tolerate junk lines
		}
		switch {
		case name == "BEGIN" && value == "VEVENT":
			ev = &Event{}
		case name == "END" && value == "VEVENT":
			if ev != nil && !ev.Start.IsZero() {
				if ev.End.IsZero() {
					if ev.AllDay {
						ev.End = ev.Start.AddDate(0, 0, 1)
					} else {
						ev.End = ev.Start
					}
				}
				cal.Events = append(cal.Events, *ev)
			}
			ev = nil
		case name == "X-WR-CALNAME" && ev == nil:
			cal.Name = value
		case ev == nil:
			// outside VEVENT: ignore
		case name == "SUMMARY":
			ev.Summary = unescape(value)
		case name == "UID":
			ev.UID = value
		case name == "LOCATION":
			ev.Location = unescape(value)
		case name == "DESCRIPTION":
			ev.Description = unescape(value)
		case name == "URL":
			ev.URL = value
		case name == "ORGANIZER":
			ev.Organizer = Attendee{Name: params["CN"], Email: stripMailto(value)}
		case name == "ATTENDEE":
			ev.Attendees = append(ev.Attendees, Attendee{Name: params["CN"], Email: stripMailto(value)})
		case name == "DTSTART":
			t, allDay, err := parseDateTime(value, params, floating)
			if err != nil {
				return fmt.Errorf("line %d: DTSTART: %w", lineNo, err)
			}
			ev.Start, ev.AllDay = t, allDay
		case name == "DTEND":
			t, _, err := parseDateTime(value, params, floating)
			if err != nil {
				return fmt.Errorf("line %d: DTEND: %w", lineNo, err)
			}
			ev.End = t
		case name == "DURATION":
			d, err := parseISODuration(value)
			if err != nil {
				return fmt.Errorf("line %d: DURATION: %w", lineNo, err)
			}
			if !ev.Start.IsZero() {
				ev.End = ev.Start.Add(d)
			}
		case name == "RRULE":
			ev.Recur = parseRRule(value, floating)
		case name == "EXDATE":
			for _, v := range strings.Split(value, ",") {
				if t, _, err := parseDateTime(v, params, floating); err == nil {
					ev.ExDates = append(ev.ExDates, t)
				}
			}
		case name == "TRANSP":
			ev.Transparent = value == "TRANSPARENT"
		case name == "STATUS":
			ev.Cancelled = value == "CANCELLED"
		}
		return nil
	}

	for sc.Scan() {
		lineNo++
		raw := strings.TrimRight(sc.Text(), "\r")
		if len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t') {
			pending += raw[1:] // folded continuation
			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		pending = raw
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return cal, nil
}

// Busy expands every opaque, non-cancelled event within the window into a
// normalized busy set.
func (c *Calendar) Busy(window interval.Span) interval.Set {
	var spans []interval.Span
	for _, ev := range c.Events {
		if ev.Transparent || ev.Cancelled {
			continue
		}
		spans = append(spans, ev.Occurrences(window)...)
	}
	return interval.Normalize(spans)
}

// Occurrence is one concrete instance of an event.
type Occurrence struct {
	Start, End time.Time
	Event      Event
}

// OccurrencesIn expands every non-cancelled event (including transparent
// ones — an agenda still shows them) into concrete instances overlapping the
// window, sorted by start time.
func (c *Calendar) OccurrencesIn(window interval.Span) []Occurrence {
	var out []Occurrence
	for _, ev := range c.Events {
		if ev.Cancelled {
			continue
		}
		for _, sp := range ev.Occurrences(window) {
			out = append(out, Occurrence{Start: sp.Start, End: sp.End, Event: ev})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Occurrences expands the event's recurrence into concrete spans overlapping
// the window.
func (ev Event) Occurrences(window interval.Span) []interval.Span {
	dur := ev.End.Sub(ev.Start)
	if dur < 0 {
		return nil
	}
	emit := func(start time.Time) (interval.Span, bool) {
		sp := interval.Span{Start: start, End: start.Add(dur)}
		for _, x := range ev.ExDates {
			if x.Equal(start) || (ev.AllDay && sameDate(x, start)) {
				return sp, false
			}
		}
		return sp, sp.Overlaps(window)
	}

	r := ev.Recur
	if r == nil || (r.Freq != "DAILY" && r.Freq != "WEEKLY") {
		if sp, ok := emit(ev.Start); ok {
			return []interval.Span{sp}
		}
		return nil
	}

	itv := r.Interval
	if itv <= 0 {
		itv = 1
	}
	byDay := r.ByDay
	if r.Freq == "WEEKLY" && len(byDay) == 0 {
		byDay = []time.Weekday{ev.Start.Weekday()}
	}
	inByDay := func(d time.Weekday) bool {
		for _, w := range byDay {
			if w == d {
				return true
			}
		}
		return false
	}

	loc := ev.Start.Location()
	start := ev.Start.In(loc)
	day0 := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	clock := start.Sub(day0)
	week0 := day0.AddDate(0, 0, -mondayIndex(day0.Weekday()))

	var out []interval.Span
	count := 0
	const iterCap = 100000
	for i := 0; i < iterCap; i++ {
		day := day0.AddDate(0, 0, i)
		switch r.Freq {
		case "DAILY":
			if i%itv != 0 {
				continue
			}
		case "WEEKLY":
			weeks := daysBetween(week0, day) / 7
			if weeks%itv != 0 || !inByDay(day.Weekday()) {
				continue
			}
		}
		occ := day.Add(clock)
		if occ.Before(ev.Start) {
			continue
		}
		count++
		if r.Count > 0 && count > r.Count {
			break
		}
		if !r.Until.IsZero() && occ.After(r.Until) {
			break
		}
		if occ.After(window.End) {
			break
		}
		if sp, ok := emit(occ); ok {
			out = append(out, sp)
		}
	}
	return out
}

func mondayIndex(d time.Weekday) int { return (int(d) + 6) % 7 }

// daysBetween counts calendar days between two local midnights, rounding so
// DST transitions (23h/25h days) don't skew the count.
func daysBetween(a, b time.Time) int {
	return int(math.Round(b.Sub(a).Hours() / 24))
}

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// splitProperty divides "NAME;PARAM=V;PARAM=V:value", respecting quoted
// parameter values.
func splitProperty(line string) (name string, params map[string]string, value string, ok bool) {
	inQuote := false
	colon := -1
	for i, c := range line {
		if c == '"' {
			inQuote = !inQuote
		}
		if c == ':' && !inQuote {
			colon = i
			break
		}
	}
	if colon < 0 {
		return "", nil, "", false
	}
	head, value := line[:colon], line[colon+1:]
	parts := splitUnquoted(head, ';')
	name = strings.ToUpper(parts[0])
	params = map[string]string{}
	for _, p := range parts[1:] {
		if k, v, found := strings.Cut(p, "="); found {
			params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return name, params, value, true
}

func splitUnquoted(s string, sep rune) []string {
	var out []string
	inQuote := false
	last := 0
	for i, c := range s {
		if c == '"' {
			inQuote = !inQuote
		}
		if c == sep && !inQuote {
			out = append(out, s[last:i])
			last = i + 1
		}
	}
	return append(out, s[last:])
}

func parseDateTime(v string, params map[string]string, floating *time.Location) (t time.Time, allDay bool, err error) {
	v = strings.TrimSpace(v)
	if params["VALUE"] == "DATE" || len(v) == 8 {
		t, err = time.ParseInLocation("20060102", v, locFor(params, floating))
		return t, true, err
	}
	if strings.HasSuffix(v, "Z") {
		t, err = time.Parse("20060102T150405Z", v)
		return t, false, err
	}
	t, err = time.ParseInLocation("20060102T150405", v, locFor(params, floating))
	return t, false, err
}

func locFor(params map[string]string, floating *time.Location) *time.Location {
	tzid := params["TZID"]
	if tzid == "" {
		return floating
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc
	}
	return floating // non-IANA TZID (e.g. Windows names): best effort
}

// parseISODuration handles RFC 5545 durations: [+-]P[nW][nD][T[nH][nM][nS]].
func parseISODuration(v string) (time.Duration, error) {
	s := strings.ToUpper(strings.TrimSpace(v))
	neg := false
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	} else {
		s = strings.TrimPrefix(s, "+")
	}
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("bad duration %q", v)
	}
	s = s[1:]
	var d time.Duration
	inTime := false
	num := 0
	haveNum := false
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			num = num*10 + int(c-'0')
			haveNum = true
		case c == 'T':
			inTime = true
		default:
			if !haveNum {
				return 0, fmt.Errorf("bad duration %q", v)
			}
			switch {
			case c == 'W':
				d += time.Duration(num) * 7 * 24 * time.Hour
			case c == 'D':
				d += time.Duration(num) * 24 * time.Hour
			case c == 'H' && inTime:
				d += time.Duration(num) * time.Hour
			case c == 'M' && inTime:
				d += time.Duration(num) * time.Minute
			case c == 'S' && inTime:
				d += time.Duration(num) * time.Second
			default:
				return 0, fmt.Errorf("bad duration %q", v)
			}
			num, haveNum = 0, false
		}
	}
	if neg {
		d = -d
	}
	return d, nil
}

var weekdayCodes = map[string]time.Weekday{
	"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday,
}

func parseRRule(v string, floating *time.Location) *Recurrence {
	r := &Recurrence{Interval: 1}
	for _, part := range strings.Split(v, ";") {
		k, val, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		switch strings.ToUpper(k) {
		case "FREQ":
			r.Freq = strings.ToUpper(val)
		case "INTERVAL":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				r.Interval = n
			}
		case "COUNT":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				r.Count = n
			}
		case "UNTIL":
			if t, _, err := parseDateTime(val, nil, floating); err == nil {
				r.Until = t
			}
		case "BYDAY":
			for _, code := range strings.Split(strings.ToUpper(val), ",") {
				// strip ordinal prefixes like 2MO, -1FR (monthly forms)
				code = strings.TrimLeft(code, "+-0123456789")
				if d, ok := weekdayCodes[code]; ok {
					r.ByDay = append(r.ByDay, d)
				}
			}
		}
	}
	return r
}

func stripMailto(s string) string {
	if len(s) >= 7 && strings.EqualFold(s[:7], "mailto:") {
		return s[7:]
	}
	return s
}

func unescape(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)
	return r.Replace(s)
}
