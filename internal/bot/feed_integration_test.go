package bot

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Optional diagnostic checks against the public feed. These never use Telegram.
// Normal CI uses synthetic fixtures and does not depend on the external service.
func TestRealFeedFetch(t *testing.T) {
	if os.Getenv("TEST_LIVE_FEED") != "1" {
		t.Skip("set TEST_LIVE_FEED=1 for the public MITECO feed")
	}
	p := PriceSource{HTTP: NewPriceHTTPClient()}
	snapshot, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Parsed %d stations", len(snapshot.Stations))
}
func TestRealFeedHistory(t *testing.T) {
	filename := os.Getenv("TEST_FEED_FILE")
	if filename == "" {
		t.Skip("set TEST_FEED_FILE to a downloaded public feed")
	}
	store := testStore(t)
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer server.Close()
	p := PriceSource{HTTP: server.Client(), URL: server.URL, Store: store}
	snapshot, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Stored history for %d stations", len(snapshot.Stations))
}
