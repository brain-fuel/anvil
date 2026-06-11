package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestDemoEndToEnd(t *testing.T) {
	srv, err := Demo("")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(path string) (int, string) {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}

	code, html := get("/l/intro")
	if code != 200 || !strings.Contains(html, "Intro with Mike") {
		t.Fatalf("booking page %d", code)
	}
	starts := regexp.MustCompile(`data-start="([^"]+)"`).FindAllStringSubmatch(html, -1)
	if len(starts) == 0 {
		t.Fatal("demo offers no slots")
	}
	first := starts[0][1]

	resp, err := http.PostForm(ts.URL+"/l/intro/book", url.Values{
		"start": {first}, "name": {"Visitor"}, "email": {"v@example.org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "meet.jit.si/anvil-demo") {
		t.Fatalf("booking failed: %d %s", resp.StatusCode, body)
	}

	// The booked slot must vanish from the next page load.
	_, html2 := get("/l/intro")
	if strings.Contains(html2, `data-start="`+first+`"`) {
		t.Fatal("booked slot still offered")
	}

	code, js := get("/api/agenda?days=7")
	if code != 200 || !strings.Contains(js, "Standup") {
		t.Fatalf("agenda %d", code)
	}
	if !strings.Contains(js, "Visitor") {
		t.Error("booked event missing from agenda")
	}

	code, _ = get("/healthz")
	if code != 200 {
		t.Fatalf("healthz %d", code)
	}
}
