package license

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// fakePolar returns a server handling activate + validate with a settable
// status.
func fakePolar(t *testing.T, status string, expires *time.Time) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		json.Unmarshal(body, &req)
		if req["organization_id"] != "org-1" {
			t.Errorf("organization_id %q", req["organization_id"])
		}
		switch r.URL.Path {
		case "/license-keys/activate":
			if req["key"] == "ANVIL_BAD" {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "act-1"})
		case "/license-keys/validate":
			if req["key"] == "ANVIL_BAD" {
				w.WriteHeader(404)
				return
			}
			resp := map[string]any{"status": status}
			if expires != nil {
				resp["expires_at"] = expires
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(404)
		}
	}))
}

func newManager(t *testing.T, api string) *Manager {
	t.Helper()
	return &Manager{
		Client: &Client{API: api, OrgID: "org-1"},
		Path:   filepath.Join(t.TempDir(), "license.json"),
	}
}

func TestActivateAndCheckGranted(t *testing.T) {
	srv := fakePolar(t, "granted", nil)
	defer srv.Close()
	m := newManager(t, srv.URL)

	st, err := m.Activate("ANVIL_GOOD", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if st.ActivationID != "act-1" || st.Status != "granted" {
		t.Fatalf("state %+v", st)
	}
	ok, _, err := m.Check()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestActivateRejectedKey(t *testing.T) {
	srv := fakePolar(t, "granted", nil)
	defer srv.Close()
	m := newManager(t, srv.URL)
	if _, err := m.Activate("ANVIL_BAD", "host-1"); err == nil || !IsInvalid(err) {
		t.Fatalf("err %v, want invalid", err)
	}
}

func TestCheckRevoked(t *testing.T) {
	srv := fakePolar(t, "granted", nil)
	m := newManager(t, srv.URL)
	if _, err := m.Activate("ANVIL_GOOD", "h"); err != nil {
		t.Fatal(err)
	}
	srv.Close()

	revoked := fakePolar(t, "revoked", nil)
	defer revoked.Close()
	m.Client.API = revoked.URL
	ok, st, err := m.Check()
	if err != nil {
		t.Fatal(err)
	}
	if ok || st.Status != "revoked" {
		t.Fatalf("ok=%v status=%s, want revoked+off", ok, st.Status)
	}
}

func TestCheckExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	srv := fakePolar(t, "granted", &past)
	defer srv.Close()
	m := newManager(t, srv.URL)
	// Seed state directly: an expired-but-granted key fails validation.
	if err := m.save(&State{Key: "ANVIL_GOOD", ActivationID: "act-1", LastValid: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ok, st, err := m.Check()
	if err != nil {
		t.Fatal(err)
	}
	if ok || st.Status != "expired" {
		t.Fatalf("ok=%v status=%s, want expired+off", ok, st.Status)
	}
}

func TestCheckOfflineGrace(t *testing.T) {
	srv := fakePolar(t, "granted", nil)
	m := newManager(t, srv.URL)
	if _, err := m.Activate("ANVIL_GOOD", "h"); err != nil {
		t.Fatal(err)
	}
	srv.Close() // API now unreachable

	// Inside grace: still licensed.
	ok, _, err := m.Check()
	if err != nil || !ok {
		t.Fatalf("inside grace: ok=%v err=%v", ok, err)
	}
	// Past grace: degraded.
	m.Now = func() time.Time { return time.Now().Add(Grace + time.Hour) }
	ok, _, err = m.Check()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("past grace should degrade to unlicensed")
	}
}

func TestCheckNoState(t *testing.T) {
	m := newManager(t, "http://127.0.0.1:9")
	ok, st, err := m.Check()
	if err != nil || ok || st != nil {
		t.Fatalf("ok=%v st=%v err=%v", ok, st, err)
	}
}

func TestNewClientEnvOverride(t *testing.T) {
	t.Setenv("ANVIL_LICENSE_API", "http://example.test")
	t.Setenv("ANVIL_LICENSE_ORG", "org-env")
	c := NewClient()
	if c.API != "http://example.test" || c.OrgID != "org-env" {
		t.Fatalf("client %+v", c)
	}
}
