package serve

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"goforge.dev/anvil/agenda"
	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
	"goforge.dev/anvil/schedule"
)

//go:embed assets
var assets embed.FS

var templates = template.Must(template.ParseFS(assets, "assets/*.html"))

// Server hosts scheduling links and the agenda app.
type Server struct {
	cfg     *Config
	loc     *time.Location
	sources map[string]Source
	writers map[string]Writer
	people  map[string]PersonConfig

	now    func() time.Time
	bookMu sync.Mutex // one booking at a time prevents double-booking races
}

// New wires a validated config into a Server.
func New(cfg *Config) (*Server, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:     cfg,
		loc:     loc,
		sources: map[string]Source{},
		writers: map[string]Writer{},
		people:  map[string]PersonConfig{},
		now:     time.Now,
	}
	for _, cal := range cfg.Calendars {
		src, err := newSource(cal, loc)
		if err != nil {
			return nil, err
		}
		s.sources[cal.Name] = src
		if w := newWriter(cal); w != nil {
			s.writers[cal.Name] = w
		}
	}
	for _, p := range cfg.People {
		s.people[p.Name] = p
	}
	return s, nil
}

// Handler returns the HTTP handler for the whole app.
func (s *Server) Handler() http.Handler {
	bookLimiter := newIPLimiter(6, 5) // 5 burst, refill 6/minute per IP
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.auth(s.handleUI))
	mux.HandleFunc("GET /api/agenda", s.auth(s.handleAgenda))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /manifest.webmanifest", s.handleAsset("assets/manifest.webmanifest", "application/manifest+json"))
	mux.HandleFunc("GET /sw.js", s.handleAsset("assets/sw.js", "text/javascript"))
	mux.HandleFunc("GET /l/{slug}", s.handleLink)
	mux.HandleFunc("POST /l/{slug}/book", bookLimiter.limit(s.handleBook))
	return logRequests(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "ok %d calendars %d links\n", len(s.sources), len(s.cfg.Links))
}

// logRequests is a one-line-per-request access log.
func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			h.ServeHTTP(w, r) // keep probes out of the log
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// ListenAndServe runs the server at cfg.Listen and shuts down gracefully on
// SIGINT/SIGTERM, letting in-flight bookings finish.
func (s *Server) ListenAndServe() error {
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Printf("anvil serve: listening on %s (%d calendars, %d links)",
		s.cfg.Listen, len(s.sources), len(s.cfg.Links))

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Print("anvil serve: shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	if s.cfg.Auth == nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(s.cfg.Auth.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(s.cfg.Auth.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="anvil"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) handleAsset(path, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := assets.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write(b)
	}
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	b, err := assets.ReadFile("assets/ui.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func (s *Server) agendaCalendars() []string {
	if len(s.cfg.Agenda) > 0 {
		return s.cfg.Agenda
	}
	names := make([]string, 0, len(s.cfg.Calendars))
	for _, c := range s.cfg.Calendars {
		names = append(names, c.Name)
	}
	return names
}

func (s *Server) handleAgenda(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 60 {
		days = 7
	}
	now := s.now().In(s.loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc)
	window := interval.Span{Start: start, End: start.AddDate(0, 0, days)}

	type result struct {
		src agenda.Source
		err error
	}
	names := s.agendaCalendars()
	results := make([]result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			cal, err := s.sources[name].Events(window)
			results[i] = result{agenda.Source{Name: name, Calendar: cal}, err}
		}(i, name)
	}
	wg.Wait()

	var srcs []agenda.Source
	var errs []string
	for i, res := range results {
		if res.err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", names[i], res.err))
			continue
		}
		srcs = append(srcs, res.src)
	}
	items := agenda.Build(srcs, window)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"timezone": s.cfg.Timezone,
		"items":    items,
		"errors":   errs,
	})
}

func (s *Server) link(slug string) *LinkConfig {
	for i := range s.cfg.Links {
		if s.cfg.Links[i].Slug == slug {
			return &s.cfg.Links[i]
		}
	}
	return nil
}

// linkSlots computes bookable slots for a link, chronologically.
func (s *Server) linkSlots(l *LinkConfig) ([]schedule.Slot, error) {
	daysAhead := l.DaysAhead
	if daysAhead <= 0 {
		daysAhead = 14
	}
	minNotice := time.Duration(l.MinNoticeH) * time.Hour
	if l.MinNoticeH == 0 {
		minNotice = 24 * time.Hour
	}
	now := s.now().In(s.loc)
	window := interval.Span{Start: now.Add(minNotice), End: now.AddDate(0, 0, daysAhead)}

	hourFrom, hourTo := 9*time.Hour, 17*time.Hour
	if l.Hours != "" {
		var err error
		if hourFrom, hourTo, err = ParseHours(l.Hours); err != nil {
			return nil, err
		}
	}
	days, err := ParseDays(l.Days)
	if err != nil {
		return nil, err
	}
	step := time.Duration(l.StepM) * time.Minute
	if step <= 0 {
		step = 30 * time.Minute
	}
	var travel time.Duration
	if l.InPerson {
		travel = time.Duration(l.TravelM) * time.Minute
	}

	busyWindow := window.Pad(travel, travel)
	person := func(name string) (schedule.Person, error) {
		p := s.people[name]
		sets := make([]interval.Set, 0, len(p.Calendars))
		for _, cn := range p.Calendars {
			set, err := s.sources[cn].Busy(busyWindow)
			if err != nil {
				return schedule.Person{}, fmt.Errorf("calendar %s: %w", cn, err)
			}
			sets = append(sets, set)
		}
		return schedule.Merge(name, sets...), nil
	}
	var required, optional []schedule.Person
	for _, n := range l.Required {
		p, err := person(n)
		if err != nil {
			return nil, err
		}
		required = append(required, p)
	}
	for _, n := range l.Optional {
		p, err := person(n)
		if err != nil {
			return nil, err
		}
		optional = append(optional, p)
	}

	slots, err := schedule.Find(schedule.Request{
		Duration: time.Duration(l.DurationM) * time.Minute,
		Window:   window,
		Location: s.loc,
		HourFrom: hourFrom,
		HourTo:   hourTo,
		Days:     days,
		Step:     step,
		Travel:   travel,
		Required: required,
		Optional: optional,
		Limit:    500,
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Start.Before(slots[j].Start) })
	return slots, nil
}

type dayGroup struct {
	Label string
	Slots []slotView
}

type slotView struct {
	Value string // RFC3339, posted back on booking
	Label string
}

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	l := s.link(r.PathValue("slug"))
	if l == nil {
		http.NotFound(w, r)
		return
	}
	slots, err := s.linkSlots(l)
	if err != nil {
		log.Printf("anvil serve: link %s: %v", l.Slug, err)
		http.Error(w, "could not load calendars — try again shortly", http.StatusBadGateway)
		return
	}
	var groups []dayGroup
	for _, slot := range slots {
		start := slot.Start.In(s.loc)
		label := start.Format("Monday, January 2")
		if len(groups) == 0 || groups[len(groups)-1].Label != label {
			groups = append(groups, dayGroup{Label: label})
		}
		g := &groups[len(groups)-1]
		g.Slots = append(g.Slots, slotView{
			Value: slot.Start.UTC().Format(time.RFC3339),
			Label: start.Format("3:04 PM"),
		})
	}
	s.render(w, "booking.html", map[string]any{
		"Link":     l,
		"Groups":   groups,
		"Timezone": s.cfg.Timezone,
		"Duration": time.Duration(l.DurationM) * time.Minute,
	})
}

func (s *Server) handleBook(w http.ResponseWriter, r *http.Request) {
	l := s.link(r.PathValue("slug"))
	if l == nil {
		http.NotFound(w, r)
		return
	}
	start, err := time.Parse(time.RFC3339, r.FormValue("start"))
	name := r.FormValue("name")
	email := r.FormValue("email")
	if err != nil || name == "" || email == "" {
		http.Error(w, "need start, name, and email", http.StatusBadRequest)
		return
	}

	s.bookMu.Lock()
	defer s.bookMu.Unlock()

	// Re-verify against live calendars: the slot must still be offered.
	slots, err := s.linkSlots(l)
	if err != nil {
		log.Printf("anvil serve: book %s: %v", l.Slug, err)
		http.Error(w, "could not load calendars — try again shortly", http.StatusBadGateway)
		return
	}
	var chosen *schedule.Slot
	for i := range slots {
		if slots[i].Start.Equal(start) {
			chosen = &slots[i]
			break
		}
	}
	if chosen == nil {
		http.Error(w, "that time was just taken — pick another", http.StatusConflict)
		return
	}

	ev := ics.Event{
		UID:     ics.NewUID(),
		Summary: fmt.Sprintf("%s: %s", l.Title, name),
		Start:   chosen.Start,
		End:     chosen.End,
		Stamp:   s.now(),
		Description: fmt.Sprintf("Booked by %s <%s> via anvil scheduling link /l/%s.",
			name, email, l.Slug),
		Attendees: []ics.Attendee{{Name: name, Email: email}},
	}
	orgName := l.Organizer
	if orgName == "" {
		orgName = l.Required[0]
	}
	if org := s.people[orgName]; org.Email != "" {
		ev.Organizer = ics.Attendee{Name: org.Name, Email: org.Email}
	}
	seen := map[string]bool{email: true}
	for _, n := range append(append([]string{}, l.Required...), chosen.Attending...) {
		p := s.people[n]
		if p.Email == "" || seen[p.Email] {
			continue
		}
		seen[p.Email] = true
		ev.Attendees = append(ev.Attendees, ics.Attendee{Name: p.Name, Email: p.Email})
	}
	switch {
	case l.InPerson:
		ev.Location = l.Address
	case l.VideoURL != "":
		ev.Location = l.VideoURL
		ev.URL = l.VideoURL
	}

	joinURL, err := s.writers[l.BookInto].CreateEvent(ev, l.GoogleMeet)
	if err != nil {
		log.Printf("anvil serve: create event for %s: %v", l.Slug, err)
		http.Error(w, "could not create the invite — try again", http.StatusBadGateway)
		return
	}
	if joinURL == "" {
		joinURL = l.VideoURL
	}

	s.render(w, "confirm.html", map[string]any{
		"Link":     l,
		"Start":    chosen.Start.In(s.loc).Format("Monday, January 2 at 3:04 PM"),
		"Timezone": s.cfg.Timezone,
		"JoinURL":  joinURL,
		"Address":  l.Address,
		"Email":    email,
	})
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("anvil serve: template %s: %v", name, err)
	}
}
