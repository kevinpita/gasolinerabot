package bot

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

// These synthetic fixtures were generated with the original Python functions.
// They contain no real chat IDs, preferences, or source-database records.
func TestLegacyDigestParity(t *testing.T) {
	want, e := os.ReadFile("testdata/legacy-digest.txt")
	if e != nil {
		t.Fatal(e)
	}
	p := defaults(1)
	p.Lat = 40
	p.Lon = -3
	got := strings.Join(digest(rankings(sampleStations(), p), 20, "14/09/2026 08:00", ""), "")
	if got != string(want) {
		t.Fatalf("legacy message differs\ngot:\n%s\nwant:\n%s", got, want)
	}
}
func TestLegacySavingsParity(t *testing.T) {
	data, e := os.ReadFile("testdata/legacy-savings.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixtures []struct {
		Kind, Mode, Best, Message string
		Value, Net                float64
		Routes                    map[string]float64
	}
	if e = json.Unmarshal(data, &fixtures); e != nil {
		t.Fatal(e)
	}
	for _, f := range fixtures {
		a := analyze(nearby(sampleStations(), 40, -3, 20, "gasolina95"), "gasolina95", f.Kind, f.Value, 6, f.Routes, f.Mode, "14/09/2026 08:00")
		if a.Best.Station.ID != f.Best || math.Abs(a.Best.Net-f.Net) > 1e-9 {
			t.Fatalf("legacy calculation differs: %+v", f)
		}
		if got := savingsMessage(a); got != f.Message {
			t.Fatalf("legacy savings message differs\ngot:\n%s\nwant:\n%s", got, f.Message)
		}
	}
}
