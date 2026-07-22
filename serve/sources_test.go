package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"goforge.dev/anvil/interval"
)

func TestICSURLSourceUsesTypedHTTPClient(t *testing.T) {
	const calendar = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s", request.Method)
		}
		_, _ = writer.Write([]byte(calendar))
	}))
	defer server.Close()

	source := &icsURLSource{url: server.URL, loc: time.UTC}
	if _, err := source.Events(interval.Span{}); err != nil {
		t.Fatal(err)
	}
}

func TestICSURLSourceClassifiesHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	source := &icsURLSource{url: server.URL, loc: time.UTC}
	_, err := source.Events(interval.Span{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("error = %v", err)
	}
}
