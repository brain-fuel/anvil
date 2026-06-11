package gcal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
)

func newTestClient(t *testing.T, api http.HandlerFunc) (*Client, *int) {
	t.Helper()
	refreshes := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "rt-1" {
			t.Errorf("bad token request: %v", r.Form)
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "expires_in": 3600})
	}))
	t.Cleanup(tokenSrv.Close)
	apiSrv := httptest.NewServer(api)
	t.Cleanup(apiSrv.Close)
	return &Client{
		ClientID: "cid", ClientSecret: "cs", RefreshToken: "rt-1",
		TokenURL: tokenSrv.URL, APIBase: apiSrv.URL,
	}, &refreshes
}

func TestBusyAndTokenCache(t *testing.T) {
	calls := 0
	c, refreshes := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer at-1" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/freeBusy" {
			t.Errorf("path %s", r.URL.Path)
		}
		var req struct{ TimeMin, TimeMax string }
		json.NewDecoder(r.Body).Decode(&req)
		if !strings.HasPrefix(req.TimeMin, "2026-06-15") {
			t.Errorf("timeMin %s", req.TimeMin)
		}
		io.WriteString(w, `{"calendars":{"primary":{"busy":[
			{"start":"2026-06-15T13:00:00Z","end":"2026-06-15T14:00:00Z"},
			{"start":"2026-06-15T13:30:00Z","end":"2026-06-15T15:00:00Z"}]}}}`)
	})
	window := interval.Span{
		Start: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC),
	}
	busy, err := c.Busy("primary", window)
	if err != nil {
		t.Fatal(err)
	}
	if len(busy) != 1 || !busy[0].End.Equal(time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("busy %v (overlaps should merge)", busy)
	}
	if _, err := c.Busy("primary", window); err != nil {
		t.Fatal(err)
	}
	if *refreshes != 1 {
		t.Errorf("token refreshed %d times, want 1 (cached)", *refreshes)
	}
	if calls != 2 {
		t.Errorf("api calls %d", calls)
	}
}

func TestCreateEventWithMeet(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/calendars/primary/events" {
			t.Errorf("path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("conferenceDataVersion") != "1" || q.Get("sendUpdates") != "all" {
			t.Errorf("query %v", q)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["summary"] != "Intro" {
			t.Errorf("summary %v", body["summary"])
		}
		cd, _ := body["conferenceData"].(map[string]any)
		if cd == nil {
			t.Error("missing conferenceData")
		}
		att, _ := body["attendees"].([]any)
		if len(att) != 1 {
			t.Errorf("attendees %v", body["attendees"])
		}
		io.WriteString(w, `{"id":"ev1","htmlLink":"https://calendar.google.com/event?eid=x",
			"hangoutLink":"https://meet.google.com/abc-defg-hij"}`)
	})
	created, err := c.CreateEvent("primary", ics.Event{
		Summary:   "Intro",
		Start:     time.Date(2026, 6, 17, 17, 30, 0, 0, time.UTC),
		End:       time.Date(2026, 6, 17, 18, 15, 0, 0, time.UTC),
		Attendees: []ics.Attendee{{Email: "guest@example.org"}},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if created.MeetLink != "https://meet.google.com/abc-defg-hij" || created.ID != "ev1" {
		t.Fatalf("created %+v", created)
	}
}

func TestEvents(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("singleEvents") != "true" {
			t.Errorf("query %v", r.URL.Query())
		}
		io.WriteString(w, `{"items":[
			{"summary":"Sync","hangoutLink":"https://meet.google.com/x",
			 "start":{"dateTime":"2026-06-17T14:00:00Z"},"end":{"dateTime":"2026-06-17T14:30:00Z"}},
			{"summary":"Offsite","start":{"date":"2026-06-18"},"end":{"date":"2026-06-19"}},
			{"summary":"Gone","status":"cancelled",
			 "start":{"dateTime":"2026-06-17T15:00:00Z"},"end":{"dateTime":"2026-06-17T16:00:00Z"}}]}`)
	})
	cal, err := c.Events("primary", interval.Span{
		Start: time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Events) != 2 {
		t.Fatalf("events %+v", cal.Events)
	}
	if cal.Events[0].URL != "https://meet.google.com/x" || !cal.Events[1].AllDay {
		t.Errorf("events %+v", cal.Events)
	}
}

func TestBusyErrorSurfaced(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"calendars":{"primary":{"errors":[{"reason":"notFound"}]}}}`)
	})
	_, err := c.Busy("primary", interval.Span{
		Start: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC),
	})
	if err == nil || !strings.Contains(err.Error(), "notFound") {
		t.Fatalf("err %v", err)
	}
}
