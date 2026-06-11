package serve

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goforge.dev/anvil/ics"
)

// fakeWriter records created events.
type fakeWriter struct {
	created []ics.Event
	join    string
}

func (f *fakeWriter) CreateEvent(ev ics.Event, meet bool) (string, error) {
	f.created = append(f.created, ev)
	if meet {
		return f.join, nil
	}
	return "", nil
}

func writeICS(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// newTestServer: mike busy Wed Jun 17 9:00–12:00 UTC; melissa free.
func newTestServer(t *testing.T) (*Server, *fakeWriter) {
	t.Helper()
	dir := t.TempDir()
	mike := writeICS(t, dir, "mike.ics", `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:m1
SUMMARY:Morning block
LOCATION:https://meet.google.com/abc-defg-hij
DTSTART:20260617T090000Z
DTEND:20260617T120000Z
END:VEVENT
END:VCALENDAR`)
	melissa := writeICS(t, dir, "melissa.ics", `BEGIN:VCALENDAR
END:VCALENDAR`)

	cfg := &Config{
		Timezone: "UTC",
		Calendars: []CalendarConfig{
			{Name: "mike-cal", ICSFile: mike},
			{Name: "melissa-cal", ICSFile: melissa},
			{Name: "target", CalDAV: &CalDAVConfig{BaseURL: "http://invalid.example", CalendarURL: "http://invalid.example/c/"}},
		},
		People: []PersonConfig{
			{Name: "mike", Email: "mike@example.com", Calendars: []string{"mike-cal"}},
			{Name: "melissa", Email: "melissa@example.com", Calendars: []string{"melissa-cal"}},
		},
		Links: []LinkConfig{{
			Slug: "intro", Title: "Intro chat", DurationM: 45,
			Required:  []string{"mike", "melissa"},
			DaysAhead: 3, MinNoticeH: 1,
			VideoURL: "https://meet.jit.si/anvil-intro",
			BookInto: "target", Organizer: "mike",
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Tue Jun 16 08:00 UTC: window with 1h notice reaches Wed and Thu.
	srv.now = func() time.Time { return time.Date(2026, 6, 16, 8, 0, 0, 0, time.UTC) }
	fw := &fakeWriter{join: "https://meet.google.com/zzz"}
	srv.writers["target"] = fw
	return srv, fw
}

func TestBookingPageShowsOpenSlots(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/l/intro")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, html)
	}
	if !strings.Contains(html, "Intro chat") {
		t.Error("missing title")
	}
	// Wed 9:00–12:00 UTC busy: 9:00 and 11:30 starts must be absent, 12:00 present.
	if strings.Contains(html, `data-start="2026-06-17T09:00:00Z"`) {
		t.Error("offered a busy slot")
	}
	if strings.Contains(html, `data-start="2026-06-17T11:30:00Z"`) {
		t.Error("offered a slot overlapping busy time")
	}
	if !strings.Contains(html, `data-start="2026-06-17T12:00:00Z"`) {
		t.Errorf("missing the 12:00 slot:\n%s", html)
	}
}

func TestBookCreatesInviteWithEveryone(t *testing.T) {
	srv, fw := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.PostForm(ts.URL+"/l/intro/book", url.Values{
		"start": {"2026-06-17T12:00:00Z"},
		"name":  {"Stan Guest"},
		"email": {"stan@example.org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if len(fw.created) != 1 {
		t.Fatalf("created %d events", len(fw.created))
	}
	ev := fw.created[0]
	if !ev.Start.Equal(time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)) || ev.End.Sub(ev.Start) != 45*time.Minute {
		t.Errorf("span %v–%v", ev.Start, ev.End)
	}
	if ev.Organizer.Email != "mike@example.com" {
		t.Errorf("organizer %+v", ev.Organizer)
	}
	emails := map[string]bool{}
	for _, a := range ev.Attendees {
		emails[a.Email] = true
	}
	for _, want := range []string{"stan@example.org", "mike@example.com", "melissa@example.com"} {
		if !emails[want] {
			t.Errorf("missing attendee %s in %+v", want, ev.Attendees)
		}
	}
	if ev.Location != "https://meet.jit.si/anvil-intro" {
		t.Errorf("location %q", ev.Location)
	}
	if !strings.Contains(string(body), "https://meet.jit.si/anvil-intro") {
		t.Error("confirmation missing join link")
	}
}

func TestBookRejectsTakenSlot(t *testing.T) {
	srv, fw := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.PostForm(ts.URL+"/l/intro/book", url.Values{
		"start": {"2026-06-17T09:30:00Z"}, // inside mike's busy block
		"name":  {"Stan Guest"},
		"email": {"stan@example.org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409", resp.StatusCode)
	}
	if len(fw.created) != 0 {
		t.Fatal("event created for a conflicting slot")
	}
}

func TestAgendaAPIAndAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.cfg.Auth = &BasicAuth{Username: "mike", Password: "pw"}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/api/agenda")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/agenda?days=3", nil)
	req.SetBasicAuth("mike", "pw")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	js := string(body)
	if !strings.Contains(js, `"Morning block"`) || !strings.Contains(js, `"joinUrl":"https://meet.google.com/abc-defg-hij"`) {
		t.Errorf("agenda json: %s", js)
	}
	// booking links stay public under auth
	resp, _ = http.Get(ts.URL + "/l/intro")
	if resp.StatusCode != 200 {
		t.Fatalf("booking page status %d under auth", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestLoadConfigRejectsBadRefs(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"calendars":[{"name":"a","ics_url":"http://x"}],
		"people":[{"name":"p","calendars":["a"]}],
		"links":[{"slug":"s","title":"T","duration_m":30,"required":["p"],"book_into":"a"}]}`), 0o644)
	if _, err := LoadConfig(bad); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err %v, want read-only book_into rejection", err)
	}
}

func TestInPersonTravelPadding(t *testing.T) {
	srv, fw := newTestServer(t)
	srv.cfg.Links[0].InPerson = true
	srv.cfg.Links[0].TravelM = 60
	srv.cfg.Links[0].VideoURL = ""
	srv.cfg.Links[0].Address = "12 Main St"
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Busy ends Wed 12:00; with 60m travel the first bookable start is 13:00.
	resp, _ := http.PostForm(ts.URL+"/l/intro/book", url.Values{
		"start": {"2026-06-17T12:00:00Z"},
		"name":  {"Stan"}, "email": {"s@x.org"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("12:00 within travel pad accepted: %d", resp.StatusCode)
	}
	resp, err := http.PostForm(ts.URL+"/l/intro/book", url.Values{
		"start": {"2026-06-17T13:00:00Z"},
		"name":  {"Stan"}, "email": {"s@x.org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("13:00 rejected: %d %s", resp.StatusCode, b)
	}
	if fw.created[0].Location != "12 Main St" {
		t.Errorf("location %q", fw.created[0].Location)
	}
	if !strings.Contains(string(b), "12 Main St") {
		t.Error("confirmation missing address")
	}
}

var _ = fmt.Sprintf // keep fmt for debugging edits

func TestEntitlementGate(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.cfg.Links = append(srv.cfg.Links, LinkConfig{
		Slug: "second", Title: "Second", DurationM: 30,
		Required: []string{"mike"}, BookInto: "target",
	})
	if err := srv.CheckEntitlement(); err == nil || !strings.Contains(err.Error(), "free tier") {
		t.Fatalf("err %v, want free-tier rejection", err)
	}
	srv.SetLicensed(true)
	if err := srv.CheckEntitlement(); err != nil {
		t.Fatalf("licensed but rejected: %v", err)
	}
	srv.cfg.Links = srv.cfg.Links[:1]
	srv.SetLicensed(false)
	if err := srv.CheckEntitlement(); err != nil {
		t.Fatalf("single link should be free: %v", err)
	}
}

func TestPoweredByFooter(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func() string {
		resp, err := http.Get(ts.URL + "/l/intro")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b)
	}
	if !strings.Contains(get(), "Powered by anvil") {
		t.Error("free tier should show the footer")
	}
	srv.SetLicensed(true)
	if strings.Contains(get(), "Powered by anvil") {
		t.Error("licensed deployment should not show the footer")
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(b), "ok") {
		t.Fatalf("%d %q", resp.StatusCode, b)
	}
}

func TestBookingRateLimit(t *testing.T) {
	lim := newIPLimiter(6, 3)
	base := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	now := base
	lim.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !lim.allow("1.2.3.4") {
			t.Fatalf("burst request %d denied", i)
		}
	}
	if lim.allow("1.2.3.4") {
		t.Fatal("burst exceeded but allowed")
	}
	if !lim.allow("5.6.7.8") {
		t.Fatal("other IP throttled")
	}
	now = base.Add(15 * time.Second) // 6/min → 1.5 tokens refilled
	if !lim.allow("1.2.3.4") {
		t.Fatal("refill did not restore a token")
	}
	if lim.allow("1.2.3.4") {
		t.Fatal("only one token should have been available")
	}
}
