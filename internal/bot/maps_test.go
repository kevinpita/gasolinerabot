package bot

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMapsAndFallback(t *testing.T) {
	for _, offline := range []bool{false, true} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if offline {
				w.WriteHeader(503)
				return
			}
			img := image.NewRGBA(image.Rect(0, 0, 256, 256))
			img.Set(0, 0, color.White)
			_ = png.Encode(w, img)
		}))
		m := Maps{HTTP: srv.Client(), Tiles: srv.URL}
		p := defaults(1)
		p.Lat = 40
		p.Lon = -3
		for _, top := range [][]Ranking{nil, rankings(sampleStations(), p)} {
			data, e := m.Render(t.Context(), 40, -3, 20, top)
			if e != nil {
				t.Fatal(e)
			}
			cfg, e := png.DecodeConfig(bytes.NewReader(data))
			if e != nil || cfg.Width != 900 || cfg.Height < 650 {
				t.Fatalf("bad PNG %+v %v", cfg, e)
			}
		}
		if calls.Load() == 0 {
			t.Fatal("no tiles requested")
		}
		srv.Close()
	}
}
func TestRouteFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	r := Routing{HTTP: srv.Client(), OSRM: srv.URL, Nominatim: srv.URL}
	if _, _, ok := r.Geocode(t.Context(), "Madrid"); ok {
		t.Fatal("unexpected geocode")
	}
	routes := r.Routes(t.Context(), 40, -3, sampleStations())
	if len(routes) != 0 {
		t.Fatal("unexpected route")
	}
	a := analyze(nearby(sampleStations(), 40, -3, 20, "gasolina95"), "gasolina95", "liters", 40, 6, routes, "one_way", "")
	if a.Best.Real {
		t.Fatal("fallback called real route")
	}
}
