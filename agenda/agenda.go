// Package agenda merges upcoming events from many calendars into one
// at-a-glance list, with the link you need to join (video) or go
// (directions) attached to each item.
package agenda

import (
	"net/url"
	"regexp"
	"sort"
	"time"

	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
)

// Source is one named calendar feeding the agenda.
type Source struct {
	Name     string
	Calendar *ics.Calendar
}

// Item is one agenda entry.
type Item struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	AllDay   bool      `json:"allDay"`
	Calendar string    `json:"calendar"`
	Summary  string    `json:"summary"`
	Location string    `json:"location,omitempty"`
	JoinURL  string    `json:"joinUrl,omitempty"` // video conferencing link, if any
	MapsURL  string    `json:"mapsUrl,omitempty"` // directions link for physical locations
}

// joinRe matches the major video-conferencing URL shapes inside free text.
var joinRe = regexp.MustCompile(`https://(?:[\w.-]*\.)?(?:meet\.google\.com|zoom\.us|teams\.microsoft\.com|teams\.live\.com|webex\.com|meet\.jit\.si|whereby\.com)/[^\s<>"',]*[^\s<>"',.)]`)

// looksPhysical reports whether a LOCATION value is a place rather than a URL.
func looksPhysical(loc string) bool {
	if loc == "" {
		return false
	}
	u, err := url.Parse(loc)
	return err != nil || u.Scheme == ""
}

// Build merges every source's occurrences within the window into one sorted
// agenda.
func Build(sources []Source, window interval.Span) []Item {
	var items []Item
	for _, src := range sources {
		if src.Calendar == nil {
			continue
		}
		for _, occ := range src.Calendar.OccurrencesIn(window) {
			ev := occ.Event
			it := Item{
				Start:    occ.Start,
				End:      occ.End,
				AllDay:   ev.AllDay,
				Calendar: src.Name,
				Summary:  ev.Summary,
				Location: ev.Location,
				JoinURL:  joinLink(ev),
			}
			if it.JoinURL == "" && looksPhysical(ev.Location) {
				it.MapsURL = "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(ev.Location)
			}
			items = append(items, it)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Start.Before(items[j].Start) })
	return items
}

// joinLink finds the first conferencing URL in URL, LOCATION, then
// DESCRIPTION.
func joinLink(ev ics.Event) string {
	for _, field := range []string{ev.URL, ev.Location, ev.Description} {
		if m := joinRe.FindString(field); m != "" {
			return m
		}
	}
	return ""
}
