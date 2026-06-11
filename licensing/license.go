// Package license validates Anvil Pro license keys against Polar.sh's
// customer-portal API (no merchant secret needed — the endpoints are
// public and scoped by organization ID). State is cached on disk so a
// licensed server keeps running through network trouble: validation is
// retried daily and a cached success is honored for a 14-day grace window.
//
// Anvil's source is MIT; the gate is honest-but-real. If you strip it,
// you know what you did.
package licensing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Set at build time:
//
//	go build -ldflags "-X goforge.dev/anvil/licensing.OrgID=<polar org id>"
//
// Overridable at runtime with ANVIL_LICENSE_ORG (forks, tests).
var OrgID = ""

// DefaultAPI is Polar's customer-portal base URL. Overridable with
// ANVIL_LICENSE_API for tests and self-hosted mocks.
const DefaultAPI = "https://api.polar.sh/v1/customer-portal"

// BuyURL is where an unlicensed deployment is pointed. Overridable at build
// time once the live checkout link exists.
var BuyURL = "https://goforge.dev/anvil/#pro"

// Grace is how long a cached successful validation keeps Pro features on
// when revalidation cannot reach the API.
const Grace = 14 * 24 * time.Hour

// Client calls the Polar license endpoints.
type Client struct {
	API   string // base URL; default DefaultAPI
	OrgID string
	HTTP  *http.Client
}

// NewClient builds a client from build-time defaults and environment
// overrides.
func NewClient() *Client {
	api := os.Getenv("ANVIL_LICENSE_API")
	if api == "" {
		api = DefaultAPI
	}
	org := os.Getenv("ANVIL_LICENSE_ORG")
	if org == "" {
		org = OrgID
	}
	return &Client{API: api, OrgID: org}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Info is the verdict on a key.
type Info struct {
	Valid     bool
	Status    string // granted | revoked | disabled | expired | not_found
	ExpiresAt time.Time
}

// ErrUnreachable wraps transport-level failures: the verdict is unknown, so
// callers fall back to the grace window rather than treating the key as bad.
var ErrUnreachable = errors.New("license: validation service unreachable")

func (c *Client) post(path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.http().Post(c.API+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: %s", ErrUnreachable, resp.Status)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusUnprocessableEntity {
		return errInvalid{resp.Status}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("license: %s: %s", resp.Status, strings.TrimSpace(string(rb)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type errInvalid struct{ status string }

func (e errInvalid) Error() string { return "license: rejected: " + e.status }

// IsInvalid reports whether err is a definitive rejection (as opposed to the
// service being unreachable).
func IsInvalid(err error) bool {
	var ei errInvalid
	return errors.As(err, &ei)
}

// Activate registers this deployment as one activation of the key and
// returns the activation ID, which subsequent validations must present.
func (c *Client) Activate(key, label string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := c.post("/license-keys/activate", map[string]string{
		"key":             key,
		"organization_id": c.OrgID,
		"label":           label,
	}, &out)
	if err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("license: activation returned no id")
	}
	return out.ID, nil
}

// Validate checks the key (and activation, when present) and returns its
// current standing.
func (c *Client) Validate(key, activationID string) (*Info, error) {
	body := map[string]string{"key": key, "organization_id": c.OrgID}
	if activationID != "" {
		body["activation_id"] = activationID
	}
	var out struct {
		Status    string     `json:"status"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := c.post("/license-keys/validate", body, &out); err != nil {
		if IsInvalid(err) {
			return &Info{Valid: false, Status: "not_found"}, nil
		}
		return nil, err
	}
	info := &Info{Status: out.Status}
	if out.ExpiresAt != nil {
		info.ExpiresAt = *out.ExpiresAt
	}
	info.Valid = out.Status == "granted" &&
		(info.ExpiresAt.IsZero() || time.Now().Before(info.ExpiresAt))
	if !info.Valid && out.Status == "granted" {
		info.Status = "expired"
	}
	return info, nil
}

// State is the on-disk record of this deployment's license.
type State struct {
	Key          string    `json:"key"`
	ActivationID string    `json:"activation_id,omitempty"`
	Label        string    `json:"label,omitempty"`
	LastValid    time.Time `json:"last_valid"`       // last successful validation
	Status       string    `json:"status,omitempty"` // last known status
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// DefaultPath is where license state lives: $XDG_CONFIG_HOME/anvil or
// ~/.config/anvil.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "anvil", "license.json")
}

// Manager ties the client to the state file.
type Manager struct {
	Client *Client
	Path   string
	Now    func() time.Time // tests; defaults to time.Now
}

// NewManager builds a manager with environment-aware defaults.
func NewManager() *Manager {
	return &Manager{Client: NewClient(), Path: DefaultPath()}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Load reads the state file; a missing file returns (nil, nil).
func (m *Manager) Load() (*State, error) {
	b, err := os.ReadFile(m.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", m.Path, err)
	}
	return &st, nil
}

func (m *Manager) save(st *State) error {
	if err := os.MkdirAll(filepath.Dir(m.Path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.Path, b, 0o600)
}

// Activate activates and validates a key, then persists the state.
func (m *Manager) Activate(key, label string) (*State, error) {
	actID, err := m.Client.Activate(key, label)
	if err != nil {
		return nil, err
	}
	info, err := m.Client.Validate(key, actID)
	if err != nil {
		return nil, err
	}
	if !info.Valid {
		return nil, fmt.Errorf("license: key activated but not valid (status %s)", info.Status)
	}
	st := &State{
		Key: key, ActivationID: actID, Label: label,
		LastValid: m.now(), Status: info.Status, ExpiresAt: info.ExpiresAt,
	}
	return st, m.save(st)
}

// Check revalidates the stored key and reports whether Pro features are on.
// Decision table:
//   - no state file               → false
//   - API says valid              → true (state refreshed)
//   - API definitively rejects    → false (status recorded)
//   - API unreachable             → true iff LastValid within Grace
func (m *Manager) Check() (bool, *State, error) {
	st, err := m.Load()
	if err != nil || st == nil {
		return false, st, err
	}
	info, err := m.Client.Validate(st.Key, st.ActivationID)
	if err != nil {
		// Unknown verdict: honor the grace window.
		ok := m.now().Sub(st.LastValid) < Grace
		return ok, st, nil
	}
	st.Status = info.Status
	st.ExpiresAt = info.ExpiresAt
	if info.Valid {
		st.LastValid = m.now()
	}
	if err := m.save(st); err != nil {
		return info.Valid, st, err
	}
	return info.Valid, st, nil
}
