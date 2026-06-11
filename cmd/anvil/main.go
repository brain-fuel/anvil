// Command anvil schedules meetings across any number of people and
// calendars.
//
//	anvil find              find a slot across iCal files/URLs
//	anvil agenda            upcoming events with join links and directions
//	anvil serve             scheduling links + agenda app (see -config)
//	anvil gcal-login        obtain a Google Calendar refresh token
//	anvil caldav-calendars  list CalDAV collections for a server
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // embed the zone database: scratch containers, Windows

	"goforge.dev/anvil/agenda"
	"goforge.dev/anvil/caldav"
	"goforge.dev/anvil/gcal"
	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
	"goforge.dev/anvil/license"
	"goforge.dev/anvil/schedule"
	"goforge.dev/anvil/serve"
)

// version is stamped by the release build:
// go build -ldflags "-X main.version=v0.3.0".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "find":
		cmdFind(os.Args[2:])
	case "agenda":
		cmdAgenda(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "license":
		cmdLicense(os.Args[2:])
	case "gcal-login":
		cmdGcalLogin(os.Args[2:])
	case "caldav-calendars":
		cmdCaldavCalendars(os.Args[2:])
	case "version":
		fmt.Println("anvil", version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: anvil <command> [flags]

  find              find a meeting slot across iCal files/URLs
  agenda            upcoming events with join links and directions
  serve             scheduling links + agenda app
  license           activate or inspect an Anvil Pro license
  gcal-login        obtain a Google Calendar refresh token
  caldav-calendars  list CalDAV calendar collections
  version           print the anvil version

Run 'anvil <command> -h' for flags.`)
	os.Exit(2)
}

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

func cmdFind(args []string) {
	fs := flag.NewFlagSet("find", flag.ExitOnError)
	var (
		durFlag    = fs.Duration("d", time.Hour, "meeting duration")
		fromFlag   = fs.String("from", "", "window start date YYYY-MM-DD (default today)")
		toFlag     = fs.String("to", "", "window end date YYYY-MM-DD, inclusive (default from+7d)")
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
	fs.Parse(args)

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

	hourFrom, hourTo, err := serve.ParseHours(*hoursFlag)
	fatalIf(err, "bad -hours")
	days, err := serve.ParseDays(*daysFlag)
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

func cmdAgenda(args []string) {
	fs := flag.NewFlagSet("agenda", flag.ExitOnError)
	var (
		daysFlag = fs.Int("days", 3, "days ahead to show")
		tzFlag   = fs.String("tz", "Local", "IANA time zone for output")
	)
	var cals []personSpec
	fs.Var(personFlag{&cals}, "cal", "calendar: name=file-or-url (repeatable)")
	fs.Parse(args)

	loc, err := time.LoadLocation(*tzFlag)
	fatalIf(err, "bad -tz")
	if len(cals) == 0 {
		fatalIf(fmt.Errorf("need at least one -cal name=file-or-url"), "")
	}

	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	window := interval.Span{Start: start, End: start.AddDate(0, 0, *daysFlag)}

	var sources []agenda.Source
	for _, c := range cals {
		for _, src := range c.cals {
			cal, err := readCalendar(src, loc)
			fatalIf(err, src)
			sources = append(sources, agenda.Source{Name: c.name, Calendar: cal})
		}
	}

	items := agenda.Build(sources, window)
	if len(items) == 0 {
		fmt.Println("nothing scheduled")
		return
	}
	prevDay := ""
	for _, it := range items {
		day := it.Start.In(loc).Format("Mon Jan _2")
		if day != prevDay {
			prevDay = day
			fmt.Printf("\n%s\n", day)
		}
		when := "all day     "
		if !it.AllDay {
			when = fmt.Sprintf("%s–%s", it.Start.In(loc).Format("15:04"), it.End.In(loc).Format("15:04"))
		}
		line := fmt.Sprintf("  %s  [%s] %s", when, it.Calendar, it.Summary)
		if it.JoinURL != "" {
			line += "\n             join: " + it.JoinURL
		} else if it.MapsURL != "" {
			line += "\n             go:   " + it.MapsURL
		}
		fmt.Println(line)
	}
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgFlag := fs.String("config", "anvil.json", "path to config file")
	fs.Parse(args)

	cfg, err := serve.LoadConfig(*cfgFlag)
	fatalIf(err, "config")
	srv, err := serve.New(cfg)
	fatalIf(err, "")

	mgr := license.NewManager()
	licensed, _, err := mgr.Check()
	if err != nil {
		fmt.Fprintf(os.Stderr, "anvil: license state: %v (continuing on free tier)\n", err)
	}
	srv.SetLicensed(licensed)
	fatalIf(srv.CheckEntitlement(), "")
	go revalidateLoop(srv, mgr)

	fatalIf(srv.ListenAndServe(), "")
}

// revalidateLoop refreshes the entitlement daily so a revoked or expired
// key degrades (and a renewed one recovers) without a restart.
func revalidateLoop(srv *serve.Server, mgr *license.Manager) {
	for range time.Tick(24 * time.Hour) {
		ok, st, err := mgr.Check()
		if err != nil {
			fmt.Fprintf(os.Stderr, "anvil: license revalidation: %v\n", err)
			continue
		}
		if ok != srv.Licensed() {
			status := "(no state)"
			if st != nil {
				status = st.Status
			}
			fmt.Fprintf(os.Stderr, "anvil: license entitlement changed: licensed=%v status=%s\n", ok, status)
		}
		srv.SetLicensed(ok)
	}
}

func cmdLicense(args []string) {
	if len(args) < 1 {
		fatalIf(fmt.Errorf("usage: anvil license <activate KEY [-label NAME] | status>"), "")
	}
	mgr := license.NewManager()
	switch args[0] {
	case "activate":
		fs := flag.NewFlagSet("license activate", flag.ExitOnError)
		labelFlag := fs.String("label", "", "label for this deployment (default hostname)")
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fatalIf(fmt.Errorf("usage: anvil license activate KEY [-label NAME]"), "")
		}
		key := args[1]
		fs.Parse(args[2:])
		label := *labelFlag
		if label == "" {
			label, _ = os.Hostname()
		}
		st, err := mgr.Activate(key, label)
		fatalIf(err, "activate")
		fmt.Printf("activated: %s (status %s", st.Label, st.Status)
		if !st.ExpiresAt.IsZero() {
			fmt.Printf(", renews/expires %s", st.ExpiresAt.Format("2006-01-02"))
		}
		fmt.Printf(")\nstate: %s\n", mgr.Path)
	case "status":
		ok, st, err := mgr.Check()
		fatalIf(err, "status")
		if st == nil {
			fmt.Printf("no license — free tier (1 scheduling link)\nAnvil Pro: %s\n", license.BuyURL)
			return
		}
		fmt.Printf("licensed:   %v\nstatus:     %s\nlabel:      %s\nlast valid: %s\n",
			ok, st.Status, st.Label, st.LastValid.Format(time.RFC3339))
		if !st.ExpiresAt.IsZero() {
			fmt.Printf("expires:    %s\n", st.ExpiresAt.Format("2006-01-02"))
		}
		if !ok {
			fmt.Printf("running on the free tier — renew at %s\n", license.BuyURL)
		}
	default:
		fatalIf(fmt.Errorf("unknown license subcommand %q (want activate or status)", args[0]), "")
	}
}

func cmdGcalLogin(args []string) {
	fs := flag.NewFlagSet("gcal-login", flag.ExitOnError)
	idFlag := fs.String("client-id", "", "OAuth client ID (Desktop app)")
	secretFlag := fs.String("client-secret", "", "OAuth client secret")
	fs.Parse(args)
	if *idFlag == "" || *secretFlag == "" {
		fatalIf(fmt.Errorf("need -client-id and -client-secret (create a Desktop-app OAuth client in Google Cloud Console)"), "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	token, err := gcal.Login(ctx, *idFlag, *secretFlag, func(u string) {
		fmt.Fprintf(os.Stderr, "Open this URL to authorize:\n\n  %s\n\n", u)
	})
	fatalIf(err, "login")
	fmt.Println(token)
	fmt.Fprintln(os.Stderr, `Store this refresh token in your config under google.refresh_token.`)
}

func cmdCaldavCalendars(args []string) {
	fs := flag.NewFlagSet("caldav-calendars", flag.ExitOnError)
	urlFlag := fs.String("url", "", "CalDAV server URL, e.g. https://caldav.fastmail.com")
	userFlag := fs.String("user", "", "username")
	passFlag := fs.String("pass", "", "password or app password (or set ANVIL_CALDAV_PASS)")
	fs.Parse(args)
	if *passFlag == "" {
		*passFlag = os.Getenv("ANVIL_CALDAV_PASS")
	}
	if *urlFlag == "" || *userFlag == "" || *passFlag == "" {
		fatalIf(fmt.Errorf("need -url, -user, and -pass (or ANVIL_CALDAV_PASS)"), "")
	}
	c := &caldav.Client{BaseURL: *urlFlag, Username: *userFlag, Password: *passFlag}
	cals, err := c.FindCalendars()
	fatalIf(err, "discover")
	for _, cal := range cals {
		fmt.Printf("%-24s %s\n", cal.Name, cal.URL)
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
