package serve

import (
	"fmt"
	"strings"
	"time"
)

// ParseHours parses "HH:MM-HH:MM" into offsets from midnight.
func ParseHours(s string) (from, to time.Duration, err error) {
	parse := func(v string) (time.Duration, error) {
		t, err := time.Parse("15:04", v)
		if err != nil {
			return 0, err
		}
		return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("want HH:MM-HH:MM, got %q", s)
	}
	if from, err = parse(a); err != nil {
		return 0, 0, err
	}
	if to, err = parse(b); err != nil {
		return 0, 0, err
	}
	if to <= from {
		return 0, 0, fmt.Errorf("hours end before start in %q", s)
	}
	return from, to, nil
}

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// ParseDays parses "mon,tue,..." (empty means default weekdays → nil).
func ParseDays(s string) ([]time.Weekday, error) {
	if s == "" {
		return nil, nil
	}
	var out []time.Weekday
	for _, name := range strings.Split(strings.ToLower(s), ",") {
		name = strings.TrimSpace(name)
		if len(name) < 3 {
			return nil, fmt.Errorf("unknown day %q", name)
		}
		d, ok := dayNames[name[:3]]
		if !ok {
			return nil, fmt.Errorf("unknown day %q", name)
		}
		out = append(out, d)
	}
	return out, nil
}
