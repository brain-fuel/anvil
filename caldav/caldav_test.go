package caldav

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
)

func TestFindCalendars(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			t.Errorf("method %s", r.Method)
		}
		if u, p, _ := r.BasicAuth(); u != "mike" || p != "secret" {
			t.Errorf("auth %s:%s", u, p)
		}
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(207)
		switch {
		case strings.Contains(string(body), "current-user-principal"):
			io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:"><d:response><d:href>/</d:href>
<d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop>
<d:current-user-principal><d:href>/principals/mike/</d:href></d:current-user-principal>
</d:prop></d:propstat></d:response></d:multistatus>`)
		case strings.Contains(string(body), "calendar-home-set"):
			io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response>
<d:href>/principals/mike/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop>
<c:calendar-home-set><d:href>/calendars/mike/</d:href></c:calendar-home-set>
</d:prop></d:propstat></d:response></d:multistatus>`)
		default:
			io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
<d:response><d:href>/calendars/mike/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status>
<d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>
<d:response><d:href>/calendars/mike/work/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status>
<d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype>
<d:displayname>Work</d:displayname></d:prop></d:propstat></d:response>
</d:multistatus>`)
		}
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Username: "mike", Password: "secret"}
	cals, err := c.FindCalendars()
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 1 || cals[0].Name != "Work" || cals[0].URL != srv.URL+"/calendars/mike/work/" {
		t.Fatalf("got %+v", cals)
	}
}

func TestBusy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "REPORT" {
			t.Errorf("method %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `time-range start="20260615T000000Z"`) {
			t.Errorf("missing time-range: %s", body)
		}
		w.WriteHeader(207)
		io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
<d:response><d:href>/calendars/mike/work/a.ics</d:href><d:propstat>
<d:status>HTTP/1.1 200 OK</d:status><d:prop><c:calendar-data>BEGIN:VCALENDAR
BEGIN:VEVENT
UID:a@x
DTSTART:20260615T130000Z
DTEND:20260615T140000Z
SUMMARY:Sync
END:VEVENT
END:VCALENDAR
</c:calendar-data></d:prop></d:propstat></d:response></d:multistatus>`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	window := interval.Span{
		Start: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC),
	}
	busy, err := c.Busy(srv.URL+"/calendars/mike/work/", window)
	if err != nil {
		t.Fatal(err)
	}
	if len(busy) != 1 || !busy[0].Start.Equal(time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("busy %v", busy)
	}
}

func TestCreateEvent(t *testing.T) {
	var gotPath, gotBody, gotMatch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method %s", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody, gotMatch = r.URL.Path, string(b), r.Header.Get("If-None-Match")
		w.WriteHeader(201)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	ev := ics.Event{
		UID:     "meet1@anvil",
		Summary: "Intro",
		Start:   time.Date(2026, 6, 17, 17, 30, 0, 0, time.UTC),
		End:     time.Date(2026, 6, 17, 18, 15, 0, 0, time.UTC),
		Attendees: []ics.Attendee{
			{Name: "Guest", Email: "guest@example.org"},
		},
	}
	loc, err := c.CreateEvent(srv.URL+"/calendars/mike/work/", ev)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(loc, "/calendars/mike/work/meet1@anvil.ics") {
		t.Errorf("location %s", loc)
	}
	if gotPath != "/calendars/mike/work/meet1@anvil.ics" {
		t.Errorf("path %s", gotPath)
	}
	if gotMatch != "*" {
		t.Errorf("If-None-Match %q", gotMatch)
	}
	if !strings.Contains(gotBody, "SUMMARY:Intro") ||
		!strings.Contains(gotBody, "ATTENDEE;CN=\"Guest\"") {
		t.Errorf("body:\n%s", gotBody)
	}
}
