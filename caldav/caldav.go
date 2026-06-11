// Package caldav is a minimal CalDAV (RFC 4791) client: discover calendars,
// read busy time, and create events. Works with Fastmail, iCloud (app
// passwords), Nextcloud, Radicale, Baïkal — anything speaking standard
// CalDAV with HTTP Basic auth.
package caldav

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
)

// Client talks to one CalDAV server.
type Client struct {
	BaseURL  string // server root or well-known URL, e.g. https://caldav.fastmail.com
	Username string
	Password string
	HTTP     *http.Client
}

// Calendar is one discovered calendar collection.
type Calendar struct {
	Name string
	URL  string // absolute URL of the collection
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) request(method, target string, depth string, body string) (*http.Response, error) {
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("caldav: %s %s: %s: %s", method, target, resp.Status, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

// resolve makes href absolute against the client base URL.
func (c *Client) resolve(href string) (string, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

// multistatus is the lenient shape of a 207 response; namespace-insensitive
// matching keeps it working across servers.
type multistatus struct {
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href      string     `xml:"href"`
	Propstats []propstat `xml:"propstat"`
}

type propstat struct {
	Status string  `xml:"status"`
	Prop   davProp `xml:"prop"`
}

type davProp struct {
	DisplayName          string    `xml:"displayname"`
	CalendarData         string    `xml:"calendar-data"`
	ResourceType         innerXML  `xml:"resourcetype"`
	CurrentUserPrincipal hrefValue `xml:"current-user-principal"`
	CalendarHomeSet      hrefValue `xml:"calendar-home-set"`
}

type hrefValue struct {
	Href string `xml:"href"`
}

type innerXML struct {
	Raw string `xml:",innerxml"`
}

func parseMultistatus(r io.Reader) (*multistatus, error) {
	var ms multistatus
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&ms); err != nil {
		return nil, fmt.Errorf("caldav: bad multistatus: %w", err)
	}
	return &ms, nil
}

func (c *Client) propfind(target, depth, body string) (*multistatus, error) {
	resp, err := c.request("PROPFIND", target, depth, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseMultistatus(resp.Body)
}

// FindCalendars walks the discovery chain: current-user-principal →
// calendar-home-set → calendar collections.
func (c *Client) FindCalendars() ([]Calendar, error) {
	ms, err := c.propfind(c.BaseURL, "0",
		`<d:propfind xmlns:d="DAV:"><d:prop><d:current-user-principal/></d:prop></d:propfind>`)
	if err != nil {
		return nil, err
	}
	principal := firstHref(ms, func(p davProp) string { return p.CurrentUserPrincipal.Href })
	if principal == "" {
		return nil, fmt.Errorf("caldav: no current-user-principal at %s", c.BaseURL)
	}
	principalURL, err := c.resolve(principal)
	if err != nil {
		return nil, err
	}

	ms, err = c.propfind(principalURL, "0",
		`<d:propfind xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav"><d:prop><cal:calendar-home-set/></d:prop></d:propfind>`)
	if err != nil {
		return nil, err
	}
	home := firstHref(ms, func(p davProp) string { return p.CalendarHomeSet.Href })
	if home == "" {
		return nil, fmt.Errorf("caldav: no calendar-home-set for %s", principalURL)
	}
	homeURL, err := c.resolve(home)
	if err != nil {
		return nil, err
	}

	ms, err = c.propfind(homeURL, "1",
		`<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:displayname/></d:prop></d:propfind>`)
	if err != nil {
		return nil, err
	}
	var cals []Calendar
	for _, r := range ms.Responses {
		for _, ps := range r.Propstats {
			if !strings.Contains(ps.Prop.ResourceType.Raw, "calendar") {
				continue
			}
			u, err := c.resolve(r.Href)
			if err != nil {
				continue
			}
			name := ps.Prop.DisplayName
			if name == "" {
				name = strings.Trim(r.Href, "/")
			}
			cals = append(cals, Calendar{Name: name, URL: u})
		}
	}
	return cals, nil
}

func firstHref(ms *multistatus, pick func(davProp) string) string {
	for _, r := range ms.Responses {
		for _, ps := range r.Propstats {
			if h := pick(ps.Prop); h != "" {
				return h
			}
		}
	}
	return ""
}

const timeFmt = "20060102T150405Z"

// Events fetches all events overlapping the window from one calendar
// collection via a calendar-query REPORT.
func (c *Client) Events(calURL string, window interval.Span) (*ics.Calendar, error) {
	body := fmt.Sprintf(`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
<d:prop><c:calendar-data/></d:prop>
<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">
<c:time-range start="%s" end="%s"/>
</c:comp-filter></c:comp-filter></c:filter>
</c:calendar-query>`, window.Start.UTC().Format(timeFmt), window.End.UTC().Format(timeFmt))

	resp, err := c.request("REPORT", calURL, "1", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ms, err := parseMultistatus(resp.Body)
	if err != nil {
		return nil, err
	}
	merged := &ics.Calendar{}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstats {
			if ps.Prop.CalendarData == "" {
				continue
			}
			cal, err := ics.Parse(strings.NewReader(ps.Prop.CalendarData))
			if err != nil {
				continue // skip unparseable resources, keep the rest
			}
			merged.Events = append(merged.Events, cal.Events...)
		}
	}
	return merged, nil
}

// Busy is Events reduced to a normalized busy set.
func (c *Client) Busy(calURL string, window interval.Span) (interval.Set, error) {
	cal, err := c.Events(calURL, window)
	if err != nil {
		return nil, err
	}
	return cal.Busy(window), nil
}

// CreateEvent PUTs a new event resource into the calendar collection and
// returns its URL. Servers with scheduling support (Fastmail, iCloud,
// Nextcloud) send invitations to ATTENDEEs automatically.
func (c *Client) CreateEvent(calURL string, ev ics.Event) (string, error) {
	if ev.UID == "" {
		ev.UID = ics.NewUID()
	}
	var buf bytes.Buffer
	if err := ics.Encode(&buf, "", []ics.Event{ev}); err != nil {
		return "", err
	}
	target := strings.TrimRight(calURL, "/") + "/" + url.PathEscape(ev.UID) + ".ics"
	req, err := http.NewRequest("PUT", target, &buf)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	req.Header.Set("If-None-Match", "*") // create only, never overwrite
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("caldav: PUT %s: %s: %s", target, resp.Status, strings.TrimSpace(string(b)))
	}
	return target, nil
}
