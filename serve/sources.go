package serve

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"goforge.dev/anvil/caldav"
	"goforge.dev/anvil/gcal"
	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
	forgehttp "goforge.dev/resty"
)

// Source reads one calendar. Events powers the agenda; Busy powers
// scheduling.
type Source interface {
	Events(window interval.Span) (*ics.Calendar, error)
	Busy(window interval.Span) (interval.Set, error)
}

// Writer creates the booked invite. The returned join URL is non-empty when
// the backend minted a conferencing link (Google Meet).
type Writer interface {
	CreateEvent(ev ics.Event, meet bool) (joinURL string, err error)
}

func newSource(cfg CalendarConfig, loc *time.Location) (Source, error) {
	switch {
	case cfg.ICSURL != "":
		return &cachingSource{inner: &icsURLSource{url: cfg.ICSURL, loc: loc}, ttl: 5 * time.Minute}, nil
	case cfg.ICSFile != "":
		return &icsFileSource{path: cfg.ICSFile, loc: loc}, nil
	case cfg.CalDAV != nil:
		c := &caldav.Client{BaseURL: cfg.CalDAV.BaseURL, Username: cfg.CalDAV.Username, Password: cfg.CalDAV.Password}
		return &caldavSource{client: c, calURL: cfg.CalDAV.CalendarURL}, nil
	case cfg.Google != nil:
		c := &gcal.Client{ClientID: cfg.Google.ClientID, ClientSecret: cfg.Google.ClientSecret, RefreshToken: cfg.Google.RefreshToken}
		return &gcalSource{client: c, calID: cfg.Google.CalendarID}, nil
	}
	return nil, fmt.Errorf("calendar %q has no source", cfg.Name)
}

func newWriter(cfg CalendarConfig) Writer {
	switch {
	case cfg.CalDAV != nil:
		c := &caldav.Client{BaseURL: cfg.CalDAV.BaseURL, Username: cfg.CalDAV.Username, Password: cfg.CalDAV.Password}
		return &caldavWriter{client: c, calURL: cfg.CalDAV.CalendarURL}
	case cfg.Google != nil:
		c := &gcal.Client{ClientID: cfg.Google.ClientID, ClientSecret: cfg.Google.ClientSecret, RefreshToken: cfg.Google.RefreshToken}
		return &gcalWriter{client: c, calID: cfg.Google.CalendarID}
	}
	return nil
}

type icsURLSource struct {
	url string
	loc *time.Location
}

func (s *icsURLSource) Events(interval.Span) (*ics.Calendar, error) {
	outcome := forgehttp.Send(forgehttp.LinOf(forgehttp.Get(nil, s.url)), context.Background())
	switch result := outcome.(type) {
	case forgehttp.Succeeded:
		body := forgehttp.TakeBody(forgehttp.LinOf(result.Response))
		defer body.Close()
		return ics.ParseIn(body, s.loc)
	case forgehttp.HTTPFailed:
		status := forgehttp.Status(result.Response)
		_, _ = forgehttp.Discard(forgehttp.LinOf(result.Response))
		return nil, fmt.Errorf("GET %s: HTTP %d", s.url, status)
	case forgehttp.TransportFailed:
		return nil, result.Err
	case forgehttp.Canceled:
		return nil, result.Err
	default:
		return nil, fmt.Errorf("GET %s: unknown HTTP outcome", s.url)
	}
}

func (s *icsURLSource) Busy(window interval.Span) (interval.Set, error) {
	cal, err := s.Events(window)
	if err != nil {
		return nil, err
	}
	return cal.Busy(window), nil
}

type icsFileSource struct {
	path string
	loc  *time.Location
}

func (s *icsFileSource) Events(interval.Span) (*ics.Calendar, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ics.ParseIn(f, s.loc)
}

func (s *icsFileSource) Busy(window interval.Span) (interval.Set, error) {
	cal, err := s.Events(window)
	if err != nil {
		return nil, err
	}
	return cal.Busy(window), nil
}

type caldavSource struct {
	client *caldav.Client
	calURL string
}

func (s *caldavSource) Events(window interval.Span) (*ics.Calendar, error) {
	return s.client.Events(s.calURL, window)
}

func (s *caldavSource) Busy(window interval.Span) (interval.Set, error) {
	return s.client.Busy(s.calURL, window)
}

type gcalSource struct {
	client *gcal.Client
	calID  string
}

func (s *gcalSource) Events(window interval.Span) (*ics.Calendar, error) {
	return s.client.Events(s.calID, window)
}

func (s *gcalSource) Busy(window interval.Span) (interval.Set, error) {
	return s.client.Busy(s.calID, window)
}

type caldavWriter struct {
	client *caldav.Client
	calURL string
}

func (w *caldavWriter) CreateEvent(ev ics.Event, _ bool) (string, error) {
	_, err := w.client.CreateEvent(w.calURL, ev)
	return "", err
}

type gcalWriter struct {
	client *gcal.Client
	calID  string
}

func (w *gcalWriter) CreateEvent(ev ics.Event, meet bool) (string, error) {
	created, err := w.client.CreateEvent(w.calID, ev, meet)
	if err != nil {
		return "", err
	}
	return created.MeetLink, nil
}

// cachingSource keeps the last fetch for ttl, so a burst of slot-page views
// doesn't hammer a provider's iCal endpoint.
type cachingSource struct {
	inner Source
	ttl   time.Duration

	mu      sync.Mutex
	cal     *ics.Calendar
	fetched time.Time
}

func (s *cachingSource) Events(window interval.Span) (*ics.Calendar, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cal != nil && time.Since(s.fetched) < s.ttl {
		return s.cal, nil
	}
	cal, err := s.inner.Events(window)
	if err != nil {
		if s.cal != nil {
			return s.cal, nil // stale beats down
		}
		return nil, err
	}
	s.cal, s.fetched = cal, time.Now()
	return cal, nil
}

func (s *cachingSource) Busy(window interval.Span) (interval.Set, error) {
	cal, err := s.Events(window)
	if err != nil {
		return nil, err
	}
	return cal.Busy(window), nil
}
