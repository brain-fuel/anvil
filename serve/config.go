// Package serve is anvil's self-hostable scheduling-link server and agenda
// app. One JSON config wires calendars (iCal URLs, CalDAV, Google) to
// people, people to scheduling links, and links to the calendar the booked
// invite is written into.
package serve

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the anvil serve configuration file.
type Config struct {
	Listen    string           `json:"listen"`           // e.g. ":8080"
	Timezone  string           `json:"timezone"`         // IANA zone for pages and masks
	Auth      *BasicAuth       `json:"auth,omitempty"`   // protects / and /api; links stay public
	Agenda    []string         `json:"agenda,omitempty"` // calendar names on the agenda; default all
	Calendars []CalendarConfig `json:"calendars"`
	People    []PersonConfig   `json:"people"`
	Links     []LinkConfig     `json:"links"`
}

// BasicAuth guards the agenda UI and API.
type BasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// CalendarConfig names one calendar source. Exactly one of ICSURL, ICSFile,
// CalDAV, or Google must be set.
type CalendarConfig struct {
	Name    string        `json:"name"`
	ICSURL  string        `json:"ics_url,omitempty"`
	ICSFile string        `json:"ics_file,omitempty"`
	CalDAV  *CalDAVConfig `json:"caldav,omitempty"`
	Google  *GoogleConfig `json:"google,omitempty"`
}

// CalDAVConfig points at one CalDAV collection.
type CalDAVConfig struct {
	BaseURL     string `json:"base_url"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	CalendarURL string `json:"calendar_url"` // collection URL; find with `anvil caldav-calendars`
}

// GoogleConfig points at one Google calendar.
type GoogleConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"` // obtain with `anvil gcal-login`
	CalendarID   string `json:"calendar_id"`   // usually "primary"
}

// PersonConfig binds a person to every calendar they live by.
type PersonConfig struct {
	Name      string   `json:"name"`
	Email     string   `json:"email,omitempty"`
	Calendars []string `json:"calendars"`
}

// LinkConfig is one public scheduling link.
type LinkConfig struct {
	Slug       string   `json:"slug"` // /l/{slug}
	Title      string   `json:"title"`
	DurationM  int      `json:"duration_m"`
	Required   []string `json:"required"`               // person names; all must be free
	Optional   []string `json:"optional,omitempty"`     // scored
	DaysAhead  int      `json:"days_ahead,omitempty"`   // search horizon; default 14
	MinNoticeH int      `json:"min_notice_h,omitempty"` // earliest bookable; default 24
	Hours      string   `json:"hours,omitempty"`        // "09:00-17:00"
	Days       string   `json:"days,omitempty"`         // "mon,tue,..."; default weekdays
	StepM      int      `json:"step_m,omitempty"`       // default 30

	InPerson bool   `json:"in_person,omitempty"`
	TravelM  int    `json:"travel_m,omitempty"` // drive-time padding when in person
	Address  string `json:"address,omitempty"`  // event LOCATION when in person

	GoogleMeet bool   `json:"google_meet,omitempty"` // attach a Meet link (Google target only)
	VideoURL   string `json:"video_url,omitempty"`   // static conferencing room otherwise

	BookInto  string `json:"book_into"`           // calendar name the invite is created in
	Organizer string `json:"organizer,omitempty"` // person name; defaults to first required
}

// LoadConfig reads and validates a config file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

// Validate checks cross-references and fills nothing in; defaults apply at
// use sites.
func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	calNames := map[string]CalendarConfig{}
	for _, cal := range c.Calendars {
		if cal.Name == "" {
			return fmt.Errorf("calendar with no name")
		}
		if _, dup := calNames[cal.Name]; dup {
			return fmt.Errorf("duplicate calendar %q", cal.Name)
		}
		n := 0
		for _, set := range []bool{cal.ICSURL != "", cal.ICSFile != "", cal.CalDAV != nil, cal.Google != nil} {
			if set {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("calendar %q: set exactly one of ics_url, ics_file, caldav, google", cal.Name)
		}
		calNames[cal.Name] = cal
	}
	people := map[string]PersonConfig{}
	for _, p := range c.People {
		if p.Name == "" {
			return fmt.Errorf("person with no name")
		}
		if len(p.Calendars) == 0 {
			return fmt.Errorf("person %q has no calendars", p.Name)
		}
		for _, cn := range p.Calendars {
			if _, ok := calNames[cn]; !ok {
				return fmt.Errorf("person %q: unknown calendar %q", p.Name, cn)
			}
		}
		people[p.Name] = p
	}
	slugs := map[string]bool{}
	for _, l := range c.Links {
		if l.Slug == "" || l.Title == "" {
			return fmt.Errorf("link needs slug and title")
		}
		if slugs[l.Slug] {
			return fmt.Errorf("duplicate link slug %q", l.Slug)
		}
		slugs[l.Slug] = true
		if l.DurationM <= 0 {
			return fmt.Errorf("link %q: duration_m must be positive", l.Slug)
		}
		if len(l.Required) == 0 {
			return fmt.Errorf("link %q: needs at least one required person", l.Slug)
		}
		for _, n := range append(append([]string{}, l.Required...), l.Optional...) {
			if _, ok := people[n]; !ok {
				return fmt.Errorf("link %q: unknown person %q", l.Slug, n)
			}
		}
		target, ok := calNames[l.BookInto]
		if !ok {
			return fmt.Errorf("link %q: book_into references unknown calendar %q", l.Slug, l.BookInto)
		}
		if target.CalDAV == nil && target.Google == nil {
			return fmt.Errorf("link %q: book_into calendar %q is read-only (need caldav or google)", l.Slug, l.BookInto)
		}
		if l.GoogleMeet && target.Google == nil {
			return fmt.Errorf("link %q: google_meet needs a google book_into calendar", l.Slug)
		}
		if l.InPerson && l.VideoURL != "" {
			return fmt.Errorf("link %q: in_person and video_url are mutually exclusive", l.Slug)
		}
	}
	for _, n := range c.Agenda {
		if _, ok := calNames[n]; !ok {
			return fmt.Errorf("agenda: unknown calendar %q", n)
		}
	}
	return nil
}
