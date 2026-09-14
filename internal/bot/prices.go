package bot

import (
	"context"
	"fmt"
	"html"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Fuel struct{ Code, Label, Field, Short string }

var Fuels = []Fuel{
	{"gasolina95", "Gasolina 95", "Precio Gasolina 95 E5", "95"},
	{"gasolina98", "Gasolina 98", "Precio Gasolina 98 E5", "98"},
	{"diesel", "Diésel / Gasóleo A", "Precio Gasoleo A", "Diésel"},
	{"diesel_premium", "Diésel premium", "Precio Gasoleo Premium", "Diésel+"},
	{"glp", "GLP", "Precio Gases licuados del petróleo", "GLP"},
}

func fuel(code string) Fuel {
	for _, f := range Fuels {
		if f.Code == code {
			return f
		}
	}
	return Fuel{}
}
func normalizeFuels(codes []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, c := range codes {
		if fuel(c).Code != "" && !seen[c] {
			out = append(out, c)
			seen[c] = true
		}
	}
	if len(out) == 0 {
		return []string{"gasolina95"}
	}
	return out
}

type Station struct {
	ID, Name, Town     string
	Lat, Lon, Distance float64
	Prices             map[string]float64
}
type Ranking struct {
	Fuel     string
	Stations []Station
}

func distance(a, b, c, d float64) float64 {
	p := math.Pi / 180
	h := math.Pow(math.Sin((c-a)*p/2), 2) + math.Cos(a*p)*math.Cos(c*p)*math.Pow(math.Sin((d-b)*p/2), 2)
	return 6371.0088 * 2 * math.Asin(math.Sqrt(math.Min(1, math.Max(0, h))))
}
func nearby(stations []Station, lat, lon, radius float64, code string) []Station {
	out := []Station{}
	for _, s := range stations {
		if s.Prices[code] <= 0 {
			continue
		}
		s.Distance = distance(lat, lon, s.Lat, s.Lon)
		if s.Distance <= radius {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Distance != out[j].Distance {
			return out[i].Distance < out[j].Distance
		}
		return out[i].Prices[code] < out[j].Prices[code]
	})
	return out
}
func rankings(stations []Station, p Prefs) []Ranking {
	out := []Ranking{}
	for _, c := range p.Fuels {
		ss := nearby(stations, p.Lat, p.Lon, p.Radius, c)
		sort.SliceStable(ss, func(i, j int) bool {
			if ss[i].Prices[c] != ss[j].Prices[c] {
				return ss[i].Prices[c] < ss[j].Prices[c]
			}
			if ss[i].Distance != ss[j].Distance {
				return ss[i].Distance < ss[j].Distance
			}
			return ss[i].Name < ss[j].Name
		})
		if len(ss) > p.Top {
			ss = ss[:p.Top]
		}
		out = append(out, Ranking{c, ss})
	}
	return out
}
func mapsURL(s Station) string {
	return fmt.Sprintf("https://www.google.com/maps/search/?api=1&query=%.6f,%.6f", s.Lat, s.Lon)
}
func place(s Station) string {
	if s.Town != "" {
		return s.Town
	}
	return "Localidad no indicada"
}
func esc(s string) string { return html.EscapeString(s) }

// Split at complete HTML blocks. Cap raw UTF-8 bytes below Telegram's character limit.
func chunks(header string, blocks []string, footer string) []string {
	out := []string{}
	current := header
	for _, b := range blocks {
		if len(current)+len(b)+len(footer) > 3900 {
			out = append(out, current)
			current = header
		}
		current += b
	}
	if len(current)+len(footer) > 3900 {
		out = append(out, current)
		current = header
	}
	return append(out, current+footer)
}
func digest(top []Ranking, radius float64, updated, label string) []string {
	header := "⛽ <b>Top gasolineras baratas</b>"
	if label != "" {
		header += " · " + esc(label)
	}
	header += fmt.Sprintf("\n📍 Radio <b>%g km</b> · 🕒 %s", radius, esc(updated))
	repeated := map[string]int{}
	for _, r := range top {
		for i, s := range r.Stations {
			if i < 3 {
				repeated[s.ID]++
			}
		}
	}
	blocks := []string{}
	for _, r := range top {
		heading := "\n\n<b>▸ " + esc(fuel(r.Fuel).Label) + "</b>"
		if len(r.Stations) == 0 {
			blocks = append(blocks, heading+"\n   Sin precios en este radio.")
			continue
		}
		for i, s := range r.Stations {
			mark := fmt.Sprintf("%d.", i+1)
			if i < 3 {
				mark = []string{"🥇", "🥈", "🥉"}[i]
			}
			also := ""
			if repeated[s.ID] > 1 {
				also = " · 🔁 aparece en varios combustibles"
			}
			prefix := ""
			if i == 0 {
				prefix = heading
			}
			blocks = append(blocks, prefix+fmt.Sprintf("\n%s <b>%s</b> · <b>%.3f €/L</b>\n   📍 %.1f km · %s%s · <a href=\"%s\">Mapa</a>", mark, esc(s.Name), s.Prices[r.Fuel], s.Distance, esc(place(s)), also, mapsURL(s)))
		}
	}
	return chunks(header, blocks, "\n\n💡 Usa <b>/repostar</b> para saber si compensa ir a una de ellas.")
}

const officialURL = "https://sedeaplicaciones.minetur.gob.es/ServiciosRESTCarburantes/PreciosCarburantes/EstacionesTerrestres/"

type Snapshot struct {
	Stations []Station
	Updated  string
	Date     time.Time
}
type PriceSource struct {
	HTTP    *http.Client
	URL     string
	Store   *Store
	mu      sync.Mutex
	cached  Snapshot
	expires time.Time
}

func spanishNumber(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), ".", ""), ",", "."), 64)
}
func (p *PriceSource) Fetch(ctx context.Context) (Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.expires) {
		return p.cached, nil
	}
	endpoint := p.URL
	if endpoint == "" {
		endpoint = officialURL
	}
	var raw struct {
		Updated string              `json:"Fecha"`
		Rows    []map[string]string `json:"ListaEESSPrecio"`
	}
	if err := getJSON(ctx, p.HTTP, endpoint, &raw); err != nil {
		return Snapshot{}, err
	}
	if len(raw.Rows) == 0 {
		return Snapshot{}, fmt.Errorf("empty official price feed")
	}
	snap := Snapshot{Updated: raw.Updated}
	for _, layout := range []string{"02/01/2006 15:04:05", "02/01/2006 15:04", "02/01/2006"} {
		if t, e := time.ParseInLocation(layout, raw.Updated, madrid); e == nil {
			snap.Date = t
			break
		}
	}
	if snap.Date.IsZero() {
		return Snapshot{}, fmt.Errorf("invalid official feed date")
	}
	for _, row := range raw.Rows {
		lat, e1 := spanishNumber(row["Latitud"])
		lon, e2 := spanishNumber(row["Longitud (WGS84)"])
		if e1 != nil || e2 != nil || !validLocation(lat, lon) {
			continue
		}
		s := Station{ID: row["IDEESS"], Name: truncate(row["Rótulo"], 100), Town: truncate(row["Municipio"], 100), Lat: lat, Lon: lon, Prices: map[string]float64{}}
		if s.Name == "" {
			s.Name = "Gasolinera"
		}
		if s.Town == "" {
			s.Town = truncate(row["Localidad"], 100)
		}
		if s.Town == "" {
			s.Town = truncate(row["Provincia"], 100)
		}
		if s.ID == "" {
			s.ID = fmt.Sprintf("%s-%f-%f", s.Name, lat, lon)
		}
		for _, f := range Fuels {
			if v, e := spanishNumber(row[f.Field]); e == nil && v > 0 && v < 100 {
				s.Prices[f.Code] = v
			}
		}
		snap.Stations = append(snap.Stations, s)
	}
	if len(snap.Stations) == 0 {
		return Snapshot{}, fmt.Errorf("no valid stations in feed")
	}
	// Write each fetched snapshot once, never on a cache hit.
	if p.Store != nil {
		if err := p.Store.Record(ctx, snap); err != nil {
			return Snapshot{}, err
		}
	}
	p.cached = snap
	p.expires = time.Now().Add(30 * time.Minute)
	return snap, nil
}
func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
func validLocation(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && math.Abs(lat) <= 90 && math.Abs(lon) <= 180
}
