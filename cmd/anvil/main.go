// Command anvil finds meeting times across any number of people and
// calendars, fed by iCalendar files or URLs.
//
//	anvil find -d 45m -from 2026-06-15 -to 2026-06-20 -tz America/New_York \
//	    -who mike=work.ics,personal.ics -who melissa=https://example.com/m.ics \
//	    -opt stan=stan.ics -opt david=david.ics -travel 30m
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
	"goforge.dev/anvil/schedule"
)

type personFlag struct {
	specs *[]personSpec
}

type personSpec struct {
	name string
	cals []string
}

func (f personFlag) String() string { return "" }

func (f personFlag) Set(v string) error {
	name, cals, ok := strings.Cut(v, "=")
	if !ok || name == "" || cals == "" {
		return fmt.Errorf("want name=calendar[,calendar...], got %q", v)
	}
	*f.specs = append(*f.specs, personSpec{name: name, cals: strings.Split(cals, ",")})
	return nil
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "find" {
		fmt.Fprintln(os.Stderr, "usage: anvil find [flags]   (see anvil find -h)")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("find", flag.ExitOnError)
	var (
		durFlag    = fs.Duration("d", time.Hour, "meeting duration")
		fromFlag   = fs.String("from", "", "window start date YYYY-MM-DD (default today)")
		toFlag     = fs.String("to", "", "window end date YYYY-MM-DD, exclusive (default from+7d)")
		tzFlag     = fs.String("tz", "Local", "IANA time zone for hours and output")
		hoursFlag  = fs.String("hours", "09:00-17:00", "allowed wall-clock hours HH:MM-HH:MM")
		daysFlag   = fs.String("days", "", "allowed days, e.g. mon,tue,wed (default weekdays)")
		stepFlag   = fs.Duration("step", 30*time.Minute, "candidate start granularity")
		travelFlag = fs.Duration("travel", 0, "drive-time padding before and after (in-person)")
		limitFlag  = fs.Int("n", 10, "max slots to show")
	)
	var required, optional []personSpec
	fs.Var(personFlag{&required}, "who", "required attendee: name=cal[,cal...] (repeatable)")
	fs.Var(personFlag{&optional}, "opt", "optional attendee: name=cal[,cal...] (repeatable)")
	fs.Parse(os.Args[2:])

	loc, err := time.LoadLocation(*tzFlag)
	fatalIf(err, "bad -tz")

	now := time.Now().In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if *fromFlag != "" {
		from, err = time.ParseInLocation("2006-01-02", *fromFlag, loc)
		fatalIf(err, "bad -from")
	}
	to := from.AddDate(0, 0, 7)
	if *toFlag != "" {
		to, err = time.ParseInLocation("2006-01-02", *toFlag, loc)
		fatalIf(err, "bad -to")
		to = to.AddDate(0, 0, 1) // inclusive end date
	}
	window := interval.Span{Start: from, End: to}

	hourFrom, hourTo, err := parseHours(*hoursFlag)
	fatalIf(err, "bad -hours")
	days, err := parseDays(*daysFlag)
	fatalIf(err, "bad -days")

	if len(required) == 0 {
		fatalIf(fmt.Errorf("need at least one -who"), "")
	}

	load := func(specs []personSpec) []schedule.Person {
		people := make([]schedule.Person, 0, len(specs))
		for _, s := range specs {
			sets := make([]interval.Set, 0, len(s.cals))
			for _, src := range s.cals {
				cal, err := readCalendar(src, loc)
				fatalIf(err, src)
				sets = append(sets, cal.Busy(window.Pad(*travelFlag, *travelFlag)))
			}
			people = append(people, schedule.Merge(s.name, sets...))
		}
		return people
	}

	slots, err := schedule.Find(schedule.Request{
		Duration: *durFlag,
		Window:   window,
		Location: loc,
		HourFrom: hourFrom,
		HourTo:   hourTo,
		Days:     days,
		Step:     *stepFlag,
		Travel:   *travelFlag,
		Required: load(required),
		Optional: load(optional),
		Limit:    *limitFlag,
	})
	fatalIf(err, "")

	if len(slots) == 0 {
		fmt.Println("no slots found — widen the window or relax constraints")
		os.Exit(1)
	}
	total := len(required) + len(optional)
	for _, s := range slots {
		start := s.Start.In(loc)
		line := fmt.Sprintf("%s  %s–%s",
			start.Format("Mon Jan _2"),
			start.Format("15:04"),
			s.End.In(loc).Format("15:04 MST"))
		n := len(required) + len(s.Attending)
		if len(optional) > 0 {
			line += fmt.Sprintf("  %d/%d", n, total)
			if len(s.Missing) > 0 {
				line += "  missing: " + strings.Join(s.Missing, ", ")
			}
		}
		fmt.Println(line)
	}
}

func readCalendar(src string, floating *time.Location) (*ics.Calendar, error) {
	var r io.ReadCloser
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		resp, err := http.Get(src)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("GET %s: %s", src, resp.Status)
		}
		r = resp.Body
	} else {
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		r = f
	}
	defer r.Close()
	return ics.ParseIn(r, floating)
}

func parseHours(s string) (from, to time.Duration, err error) {
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

func parseDays(s string) ([]time.Weekday, error) {
	if s == "" {
		return nil, nil
	}
	var out []time.Weekday
	for _, name := range strings.Split(strings.ToLower(s), ",") {
		d, ok := dayNames[strings.TrimSpace(name)[:3]]
		if !ok {
			return nil, fmt.Errorf("unknown day %q", name)
		}
		out = append(out, d)
	}
	return out, nil
}

func fatalIf(err error, context string) {
	if err == nil {
		return
	}
	if context != "" {
		fmt.Fprintf(os.Stderr, "anvil: %s: %v\n", context, err)
	} else {
		fmt.Fprintf(os.Stderr, "anvil: %v\n", err)
	}
	os.Exit(1)
}
