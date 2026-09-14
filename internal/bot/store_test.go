package bot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	s, e := OpenStore(t.Context(), url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Pool.Close)
	if _, e = s.Pool.Exec(t.Context(), "TRUNCATE user_prefs,price_history"); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestStoreRoundTripAndHistory(t *testing.T) {
	s := testStore(t)
	p := defaults(42)
	p.Lat = 40
	p.Lon = -3
	if e := s.Save(t.Context(), p); e != nil {
		t.Fatal(e)
	}
	got, ok, e := s.Get(t.Context(), 42)
	if e != nil || !ok || got.Top != 5 || len(got.Fuels) != 2 {
		t.Fatalf("get %+v %v", got, e)
	}
	now := time.Now()
	if e = s.MarkAlert(t.Context(), 42, now); e != nil {
		t.Fatal(e)
	}
	p.Top = 15
	if e = s.Save(t.Context(), p); e != nil {
		t.Fatal(e)
	}
	got, _, _ = s.Get(t.Context(), 42)
	if got.LastAlert == nil {
		t.Fatal("save erased sent timestamp")
	}
	snap := Snapshot{Stations: sampleStations(), Date: now}
	for i := 0; i < 2; i++ {
		if e = s.Record(t.Context(), snap); e != nil {
			t.Fatal(e)
		}
	}
	history, e := s.History(t.Context(), "1", "gasolina95", now)
	if e != nil || len(history) != 1 || history[0].Price != 1.6 {
		t.Fatalf("history: %+v %v", history, e)
	}
	p.Top = 99
	if e = s.Save(t.Context(), p); e == nil {
		t.Fatal("DB accepted invalid top")
	}
}
func TestPreferencesImport(t *testing.T) {
	s := testStore(t)
	p := defaults(99)
	b, _ := json.Marshal([]Prefs{p})
	for i, want := range []int{1, 0} {
		n, e := s.ImportPrefs(t.Context(), strings.NewReader(string(b)))
		if e != nil || n != want {
			t.Fatalf("import %d: %d %v", i, n, e)
		}
	}
	p.ChatID = 100
	bad := p
	bad.ChatID = 101
	bad.Radius = -1
	b, _ = json.Marshal([]Prefs{p, bad})
	if _, e := s.ImportPrefs(t.Context(), strings.NewReader(string(b))); e == nil {
		t.Fatal("invalid import accepted")
	}
	if _, ok, _ := s.Get(t.Context(), 100); ok {
		t.Fatal("partial import committed")
	}
	var count int
	_ = s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM price_history").Scan(&count)
	if count != 0 {
		t.Fatal("import added history")
	}
}
func TestTelegramConversations(t *testing.T) {
	s := testStore(t)
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Content-Type"), "multipart") {
			if e := r.ParseMultipartForm(8 << 20); e != nil {
				t.Error(e)
			}
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
			sent = append(sent, r.FormValue("caption"))
		} else {
			var body struct {
				Text   string
				Markup json.RawMessage `json:"reply_markup"`
			}
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
				return
			}
			if string(body.Markup) == "null" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: object expected as reply markup"}`)
				return
			}
			sent = append(sent, body.Text)
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer srv.Close()
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer offline.Close()
	app := &App{Store: s, Telegram: &Telegram{HTTP: srv.Client(), Base: srv.URL}, Prices: &PriceSource{cached: Snapshot{Stations: sampleStations(), Updated: "14/09/2026 08:00", Date: time.Now()}, expires: time.Now().Add(time.Hour)}, Routing: &Routing{HTTP: offline.Client(), OSRM: offline.URL, Nominatim: offline.URL}, Maps: &Maps{HTTP: offline.Client(), Tiles: offline.URL}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handle := func(text, data string, loc *Location) {
		t.Helper()
		m := &Message{ID: 1, Chat: Chat{ID: 42}, From: &Chat{ID: 42}, Text: text, Location: loc}
		u := Update{Message: m}
		if data != "" {
			u = Update{Callback: &Callback{ID: "callback", From: Chat{ID: 42}, Message: m, Data: data}}
		}
		if e := app.Handle(t.Context(), u); e != nil {
			t.Fatal(e)
		}
	}
	handle("/start", "", nil)
	handle("", "", &Location{40, -3})
	handle("", "fuel:diesel", nil)
	handle("", "fuel_done", nil)
	handle("20 km", "", nil)
	handle("", "confirm_radius", nil)
	p, ok, e := s.Get(t.Context(), 42)
	if e != nil || !ok || p.Lat != 40 || len(p.Fuels) != 2 {
		t.Fatalf("setup %+v %v", p, e)
	}
	handle("/top 15", "", nil)
	handle("/precio", "", nil)
	handle("/cerca", "", nil)
	handle("", "", &Location{40.1, -3.1})
	p, _, _ = s.Get(t.Context(), 42)
	if p.Lat != 40 {
		t.Fatal("temporary location saved")
	}
	handle("/alertas", "", nil)
	handle("", "alerts_schedule:weekly:9:0", nil)
	p, _, _ = s.Get(t.Context(), 42)
	if p.Mode != "weekly" || p.Hour != 9 {
		t.Fatal("schedule not saved")
	}
	handle("/avisos_off", "", nil)
	p, _, _ = s.Get(t.Context(), 42)
	if p.Alerts {
		t.Fatal("alerts not paused")
	}
	handle("/avisos_on", "", nil)
	handle("/alertas_periodo diario 14", "", nil)
	handle("/tendencia", "", nil)
	handle("", "map:prices", nil)
	handle("/repostar", "", nil)
	handle("", "", &Location{40, -3})
	handle("", "saving_fuel:gasolina95", nil)
	handle("", "saving_amount:euros:30", nil)
	handle("", "saving_consumption:6", nil)
	p, _, _ = s.Get(t.Context(), 42)
	if p.Amount == nil || *p.Amount != 30 || p.Consumption == nil || *p.Consumption != 6 {
		t.Fatal("savings preferences not saved")
	}
	handle("/ahorro", "", nil)
	handle("/cancelar", "", nil)
	combined := strings.Join(sent, "\n")
	for _, text := range []string{"Gasolineras baratas España", "Top gasolineras baratas", "ubicación enviada", "Repostar · solo ida", "Repostar · ida y vuelta", "Tendencia últimos 7 días", "Cancelado."} {
		if !strings.Contains(combined, text) {
			t.Errorf("missing %q", text)
		}
	}
	// Stale callback must not overwrite persisted settings.
	handle("", "saving_amount:euros:999", nil)
	p, _, _ = s.Get(t.Context(), 42)
	if *p.Amount != 30 {
		t.Fatal("stale callback changed amount")
	}
}
func TestImportRejectsTrailingData(t *testing.T) {
	s := testStore(t)
	if _, e := s.ImportPrefs(context.Background(), strings.NewReader("[] {}")); e == nil {
		t.Fatal("trailing JSON accepted")
	}
}
