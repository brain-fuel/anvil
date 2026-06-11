// Package gcal is a minimal Google Calendar API client using only the
// standard library: OAuth2 refresh-token auth, free/busy queries, and event
// creation with Google Meet conference links. Use the Login helper (or
// `anvil gcal-login`) once to obtain a refresh token for a Desktop-app
// OAuth client.
package gcal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"goforge.dev/anvil/ics"
	"goforge.dev/anvil/interval"
)

// Client calls the Google Calendar API for one account.
type Client struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	HTTP         *http.Client

	// Overridable in tests; defaults to the real Google endpoints.
	TokenURL string
	APIBase  string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

const (
	defaultTokenURL = "https://oauth2.googleapis.com/token"
	defaultAPIBase  = "https://www.googleapis.com/calendar/v3"
	authURL         = "https://accounts.google.com/o/oauth2/v2/auth"
	scope           = "https://www.googleapis.com/auth/calendar"
)

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return defaultTokenURL
}

func (c *Client) apiBase() string {
	if c.APIBase != "" {
		return c.APIBase
	}
	return defaultAPIBase
}

// accessToken returns a valid bearer token, refreshing if needed.
func (c *Client) accessToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiry) {
		return c.token, nil
	}
	form := url.Values{
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"refresh_token": {c.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	resp, err := c.http().PostForm(c.tokenURL(), form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("gcal: token response: %w", err)
	}
	if tok.Error != "" || tok.AccessToken == "" {
		return "", fmt.Errorf("gcal: token refresh failed: %s %s", tok.Error, tok.ErrorDesc)
	}
	c.token = tok.AccessToken
	c.expiry = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - time.Minute)
	return c.token, nil
}

func (c *Client) do(method, path string, body any, out any) error {
	tok, err := c.accessToken()
	if err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.apiBase()+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("gcal: %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Busy queries free/busy for one calendar ("primary" or an address).
func (c *Client) Busy(calendarID string, window interval.Span) (interval.Set, error) {
	req := map[string]any{
		"timeMin": window.Start.UTC().Format(time.RFC3339),
		"timeMax": window.End.UTC().Format(time.RFC3339),
		"items":   []map[string]string{{"id": calendarID}},
	}
	var out struct {
		Calendars map[string]struct {
			Busy []struct {
				Start time.Time `json:"start"`
				End   time.Time `json:"end"`
			} `json:"busy"`
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"calendars"`
	}
	if err := c.do("POST", "/freeBusy", req, &out); err != nil {
		return nil, err
	}
	cal, ok := out.Calendars[calendarID]
	if !ok {
		return nil, fmt.Errorf("gcal: calendar %q missing from freeBusy response", calendarID)
	}
	if len(cal.Errors) > 0 {
		return nil, fmt.Errorf("gcal: freeBusy %q: %s", calendarID, cal.Errors[0].Reason)
	}
	spans := make([]interval.Span, 0, len(cal.Busy))
	for _, b := range cal.Busy {
		spans = append(spans, interval.Span{Start: b.Start, End: b.End})
	}
	return interval.Normalize(spans), nil
}

// Events lists concrete event instances (recurrence pre-expanded by the
// API) overlapping the window, mapped into an ics.Calendar so agendas can
// treat every source alike.
func (c *Client) Events(calendarID string, window interval.Span) (*ics.Calendar, error) {
	q := url.Values{
		"timeMin":      {window.Start.UTC().Format(time.RFC3339)},
		"timeMax":      {window.End.UTC().Format(time.RFC3339)},
		"singleEvents": {"true"},
		"orderBy":      {"startTime"},
		"maxResults":   {"250"},
	}
	var out struct {
		Items []struct {
			Summary     string `json:"summary"`
			Location    string `json:"location"`
			Description string `json:"description"`
			HangoutLink string `json:"hangoutLink"`
			Status      string `json:"status"`
			Start       struct {
				DateTime time.Time `json:"dateTime"`
				Date     string    `json:"date"`
			} `json:"start"`
			End struct {
				DateTime time.Time `json:"dateTime"`
				Date     string    `json:"date"`
			} `json:"end"`
		} `json:"items"`
	}
	path := "/calendars/" + url.PathEscape(calendarID) + "/events?" + q.Encode()
	if err := c.do("GET", path, nil, &out); err != nil {
		return nil, err
	}
	cal := &ics.Calendar{Name: calendarID}
	for _, it := range out.Items {
		if it.Status == "cancelled" {
			continue
		}
		ev := ics.Event{
			Summary:     it.Summary,
			Location:    it.Location,
			Description: it.Description,
			URL:         it.HangoutLink,
		}
		if it.Start.Date != "" {
			ev.AllDay = true
			ev.Start, _ = time.Parse("2006-01-02", it.Start.Date)
			ev.End, _ = time.Parse("2006-01-02", it.End.Date)
		} else {
			ev.Start, ev.End = it.Start.DateTime, it.End.DateTime
		}
		if ev.Start.IsZero() {
			continue
		}
		cal.Events = append(cal.Events, ev)
	}
	return cal, nil
}

// Created describes the event made by CreateEvent.
type Created struct {
	ID       string
	HTMLLink string
	MeetLink string
}

// CreateEvent inserts an event. With meet true, Google attaches a Meet
// conference and the link is returned; attendees receive invitations
// (sendUpdates=all).
func (c *Client) CreateEvent(calendarID string, ev ics.Event, meet bool) (*Created, error) {
	body := map[string]any{
		"summary":     ev.Summary,
		"location":    ev.Location,
		"description": ev.Description,
		"start":       map[string]string{"dateTime": ev.Start.UTC().Format(time.RFC3339)},
		"end":         map[string]string{"dateTime": ev.End.UTC().Format(time.RFC3339)},
	}
	if len(ev.Attendees) > 0 {
		var att []map[string]string
		for _, a := range ev.Attendees {
			m := map[string]string{"email": a.Email}
			if a.Name != "" {
				m["displayName"] = a.Name
			}
			att = append(att, m)
		}
		body["attendees"] = att
	}
	path := "/calendars/" + url.PathEscape(calendarID) + "/events?sendUpdates=all"
	if meet {
		body["conferenceData"] = map[string]any{
			"createRequest": map[string]any{
				"requestId":             randomID(),
				"conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"},
			},
		}
		path += "&conferenceDataVersion=1"
	}
	var out struct {
		ID          string `json:"id"`
		HTMLLink    string `json:"htmlLink"`
		HangoutLink string `json:"hangoutLink"`
	}
	if err := c.do("POST", path, body, &out); err != nil {
		return nil, err
	}
	return &Created{ID: out.ID, HTMLLink: out.HTMLLink, MeetLink: out.HangoutLink}, nil
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Login runs the OAuth2 installed-app loopback flow: starts a localhost
// listener, prints the consent URL, exchanges the code, and returns the
// refresh token. Use once per account, then store the token in config.
func Login(ctx context.Context, clientID, clientSecret string, openURL func(string)) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://%s/callback", ln.Addr())

	consent := authURL + "?" + url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirect},
		"response_type": {"code"},
		"scope":         {scope},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
	}.Encode()
	openURL(consent)

	codeCh := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", 400)
			return
		}
		fmt.Fprintln(w, "anvil: authorized — you can close this tab")
		codeCh <- code
	})}
	go srv.Serve(ln)
	defer srv.Close()

	var code string
	select {
	case code = <-codeCh:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	resp, err := http.PostForm(defaultTokenURL, url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirect},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.RefreshToken == "" {
		return "", fmt.Errorf("gcal: no refresh token: %s %s", tok.Error, tok.ErrorDesc)
	}
	return tok.RefreshToken, nil
}
