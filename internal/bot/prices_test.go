package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sampleStations() []Station {
	return []Station{{ID: "1", Name: "Cercana & Co", Town: "Madrid", Lat: 40, Lon: -3, Prices: map[string]float64{"gasolina95": 1.6, "diesel": 1.5}}, {ID: "2", Name: "Barata", Town: "Madrid", Lat: 40.01, Lon: -3, Prices: map[string]float64{"gasolina95": 1.4, "diesel": 1.3}}, {ID: "3", Name: "Lejana", Town: "Madrid", Lat: 41, Lon: -3, Prices: map[string]float64{"gasolina95": 1.1}}}
}
func TestRankingAndDigest(t *testing.T) {
	p := defaults(1)
	p.Lat = 40
	p.Lon = -3
	p.Radius = 20
	top := rankings(sampleStations(), p)
	if len(top) != 2 || len(top[0].Stations) != 2 || top[0].Stations[0].ID != "2" {
		t.Fatalf("unexpected ranking: %+v", top)
	}
	text := strings.Join(digest(top, 20, "14/09/2026 08:00", ""), "")
	for _, want := range []string{"🥇 <b>Barata</b>", "1.400 €/L", "Cercana &amp; Co", "aparece en varios combustibles", "/repostar"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
}
func TestDigestBoundaries(t *testing.T) {
	top := []Ranking{}
	for _, f := range Fuels {
		r := Ranking{Fuel: f.Code}
		for i := 0; i < 15; i++ {
			r.Stations = append(r.Stations, Station{ID: fmt.Sprint(i), Name: strings.Repeat("&", 100), Town: strings.Repeat("<", 100), Prices: map[string]float64{f.Code: 1.5}})
		}
		top = append(top, r)
	}
	messages := digest(top, 500, "14/09/2026 08:00", "")
	if len(messages) < 2 {
		t.Fatal("did not split")
	}
	for _, m := range messages {
		if len(m) > 3900 {
			t.Fatalf("message too large: %d", len(m))
		}
		if strings.Count(m, "<b>") != strings.Count(m, "</b>") {
			t.Fatal("split HTML")
		}
	}
}
func TestPricesCache(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"Fecha":"14/09/2026 08:00:00","ListaEESSPrecio":[{"IDEESS":"1","Rótulo":"Station","Latitud":"40,0","Longitud (WGS84)":"-3,0","Precio Gasolina 95 E5":"1,599"}]}`)
	}))
	defer srv.Close()
	p := &PriceSource{HTTP: srv.Client(), URL: srv.URL}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			s, e := p.Fetch(t.Context())
			if e != nil || len(s.Stations) != 1 {
				t.Errorf("fetch: %v", e)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("%d requests", calls.Load())
	}
}
func TestMalformedFeed(t *testing.T) {
	for _, body := range []string{`{}`, `{"Fecha":"no","ListaEESSPrecio":[{}]}`, `{"Fecha":"14/09/2026","ListaEESSPrecio":[{"Latitud":"NaN","Longitud (WGS84)":"0"}]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		p := PriceSource{HTTP: srv.Client(), URL: srv.URL}
		if _, e := p.Fetch(t.Context()); e == nil {
			t.Error("accepted malformed feed")
		}
		srv.Close()
	}
}
func TestSavings(t *testing.T) {
	ss := nearby(sampleStations(), 40, -3, 20, "gasolina95")
	a := analyze(ss, "gasolina95", "euros", 40, 6, map[string]float64{"1": 0, "2": 2}, "round_trip", "")
	if a.Best.Station.ID != "2" || a.Best.Route != 4 || a.Best.Liters != 25 {
		t.Fatalf("wrong analysis: %+v", a)
	}
	if a.Best.Net < 4.66 || a.Best.Net > 4.67 {
		t.Fatalf("net %f", a.Best.Net)
	}
	b := analyze(ss, "gasolina95", "liters", 1, 25, map[string]float64{"2": 100}, "one_way", "")
	if b.Best.Station.ID != "1" {
		t.Fatal("must recommend baseline when trip costs too much")
	}
	if !strings.Contains(savingsMessage(a), "~28.6 L") {
		t.Fatal("euro amount presentation changed")
	}
}
func TestInputValidation(t *testing.T) {
	for _, s := range []string{"-5", "NaN", "Inf", "10001 €", "0"} {
		if _, _, ok := amount(s); ok {
			t.Errorf("accepted %q", s)
		}
	}
	k, v, ok := amount("40,5 L")
	if !ok || k != "liters" || v != 40.5 {
		t.Fatal("Spanish liters")
	}
	if _, _, _, ok := parseSchedule("diario 9:30"); ok {
		t.Fatal("silently discarded minutes")
	}
	mode, h, d, ok := parseSchedule("semanal miércoles 9")
	if !ok || mode != "weekly" || h != 9 || d == nil || *d != 2 {
		t.Fatal("Spanish weekday")
	}
}
func TestAlertScheduleDST(t *testing.T) {
	p := defaults(1)
	p.Hour = 2
	first := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	if !due(first, p) {
		t.Fatal("first occurrence not due")
	}
	p.LastAlert = &first
	if due(second, p) {
		t.Fatal("duplicate alert during DST fallback")
	}
	p.LastAlert = nil
	p.Alerts = false
	if due(first, p) {
		t.Fatal("paused alert")
	}
}
func TestTelegramRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"secret-token"}`)
	}))
	defer srv.Close()
	tg := Telegram{HTTP: srv.Client(), Base: srv.URL + "/secret-token"}
	e := tg.Send(t.Context(), 1, "hello", nil)
	if e == nil || strings.Contains(e.Error(), "secret-token") {
		t.Fatalf("error leaks token: %v", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e = tg.Send(ctx, 1, "hello", nil)
	if e == nil || strings.Contains(e.Error(), "secret-token") {
		t.Fatal("transport leaks token")
	}
}
func TestTelegramMessageShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if body["parse_mode"] != "HTML" || body["chat_id"] != float64(42) {
			t.Error(body)
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer srv.Close()
	tg := Telegram{HTTP: srv.Client(), Base: srv.URL}
	if e := tg.Send(t.Context(), 42, "hello", mainKeys()); e != nil {
		t.Fatal(e)
	}
}
