package bot

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var numberRE = regexp.MustCompile(`\d+(?:[.,]\d+)?`)

func number(text string) (float64, bool) {
	s := numberRE.FindString(text)
	v, e := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	return v, e == nil && !strings.Contains(text, "-") && !math.IsInf(v, 0) && !math.IsNaN(v)
}
func amount(text string) (string, float64, bool) {
	v, ok := number(text)
	s := strings.ToLower(text)
	kind := "euros"
	if strings.Contains(s, "l") && !strings.Contains(s, "€") && !strings.Contains(s, "eur") {
		kind = "liters"
	}
	return kind, v, ok && v > 0 && v <= 10000
}
func amountLabel(kind string, v float64) string {
	if kind == "liters" {
		return fmt.Sprintf("%g L", v)
	}
	return fmt.Sprintf("%g €", v)
}
func bearing(a, b, c, d float64) float64 {
	p := math.Pi / 180
	y := math.Sin((d-b)*p) * math.Cos(c*p)
	x := math.Cos(a*p)*math.Sin(c*p) - math.Sin(a*p)*math.Cos(c*p)*math.Cos((d-b)*p)
	return math.Mod(math.Atan2(y, x)/p+360, 360)
}
func along(stations []Station, lat, lon, dlat, dlon float64) []Station {
	direct := distance(lat, lon, dlat, dlon)
	if direct == 0 {
		return stations
	}
	heading := bearing(lat, lon, dlat, dlon)
	out := []Station{}
	for _, s := range stations {
		diff := math.Abs(heading - bearing(lat, lon, s.Lat, s.Lon))
		diff = math.Min(diff, 360-diff)
		if diff <= 100 && s.Distance+distance(s.Lat, s.Lon, dlat, dlon) <= direct*1.35 {
			out = append(out, s)
		}
	}
	return out
}

type Saving struct {
	Station                                Station
	Price, Route, Cost, Gross, Net, Liters float64
	Real                                   bool
}
type Analysis struct {
	Fuel, Kind, Updated, Mode string
	Amount, Consumption       float64
	Options                   []Saving
	Best                      Saving
}

func analyze(candidates []Station, code, kind string, value, consumption float64, routes map[string]float64, mode, updated string) *Analysis {
	if len(candidates) == 0 {
		return nil
	}
	baseline := candidates[0]
	bp := baseline.Prices[code]
	liters := value
	if kind == "euros" {
		liters = value / bp
	}
	cheap := append([]Station(nil), candidates...)
	sort.SliceStable(cheap, func(i, j int) bool {
		if cheap[i].Prices[code] != cheap[j].Prices[code] {
			return cheap[i].Prices[code] < cheap[j].Prices[code]
		}
		return cheap[i].Distance < cheap[j].Distance
	})
	if len(cheap) > 10 {
		cheap = cheap[:10]
	}
	found := false
	for _, s := range cheap {
		if s.ID == baseline.ID {
			found = true
		}
	}
	if !found {
		cheap = append(cheap, baseline)
	}
	multiplier := 2.
	if mode == "one_way" {
		multiplier = 1
	}
	out := &Analysis{Fuel: code, Kind: kind, Amount: value, Consumption: consumption, Mode: mode, Updated: updated}
	for _, s := range cheap {
		km, real := routes[s.ID]
		if !real {
			km = s.Distance * 1.25
		}
		km *= multiplier
		p := s.Prices[code]
		cost := km * consumption / 100 * p
		gross := math.Max(0, (bp-p)*liters)
		net := gross - cost
		if s.ID == baseline.ID {
			cost = 0
			gross = 0
			net = 0
		}
		out.Options = append(out.Options, Saving{s, p, km, cost, gross, net, liters, real})
	}
	sort.SliceStable(out.Options, func(i, j int) bool {
		a, b := out.Options[i], out.Options[j]
		if a.Net != b.Net {
			return a.Net > b.Net
		}
		if a.Price != b.Price {
			return a.Price < b.Price
		}
		return a.Route < b.Route
	})
	if len(out.Options) > 5 {
		out.Options = out.Options[:5]
	}
	out.Best = out.Options[0]
	return out
}
func savingsMessage(a *Analysis) string {
	b := a.Best
	s := b.Station
	trip := "ida y vuelta"
	tripLabel := "Ida/vuelta"
	if a.Mode == "one_way" {
		trip = "solo ida"
		tripLabel = "Solo ida"
	}
	note := "✅ Es la opción que mejor combina precio y desplazamiento."
	if b.Net <= 0 {
		note = "⚠️ No veo una opción claramente mejor en el radio elegido."
	} else if b.Net <= 0.5 {
		note = "🟡 La diferencia es pequeña; solo iría si te queda de paso."
	}
	source := "estimada"
	routeNote := "🧭 Estimado desde la distancia en línea recta."
	if b.Real {
		source = "por carretera"
		routeNote = "🧭 Calculado con ruta por carretera."
	}
	text := fmt.Sprintf("🚗 <b>Repostar · %s</b>\n\n⭐ <b>%s</b> · 📍 %s\n⛽ %s · <b>%.3f €/L</b>\n📍 %.1f km en línea recta · <a href=\"%s\">Abrir en Maps</a>\n🛣 %s %s: <b>%.1f km</b>\n", trip, esc(s.Name), esc(place(s)), esc(fuel(a.Fuel).Label), b.Price, s.Distance, mapsURL(s), tripLabel, source, b.Route)
	if a.Kind == "euros" {
		text += fmt.Sprintf("💶 <b>%g € - %.3f €/L</b>\n🧮 Litros en la gasolinera recomendada: <b>~%.1f L</b>", a.Amount, b.Price, a.Amount/b.Price)
	} else {
		text += fmt.Sprintf("💶 Repostaje: <b>%g L</b>\n⛽ Precio gasolinera recomendada: <b>%.3f €/L</b>", a.Amount, b.Price)
	}
	text += fmt.Sprintf("\n🔥 Consumo usado: <b>%g L/100 km</b>\n%s\n\n%s\n\n<b>Top recomendado · %s</b>", a.Consumption, routeNote, note, trip)
	for i, o := range a.Options {
		mark := fmt.Sprintf("%d.", i+1)
		if o.Station.ID == b.Station.ID {
			mark = "⭐"
		}
		src := "estim."
		if o.Real {
			src = "carretera"
		}
		text += fmt.Sprintf("\n%s %s · %s · %.3f €/L · %.1f km %s", mark, esc(o.Station.Name), esc(place(o.Station)), o.Price, o.Route, src)
	}
	if a.Updated != "" {
		text += "\n\nDatos oficiales: " + esc(a.Updated)
	}
	return text
}

type cachedRoute struct {
	KM      float64
	Expires time.Time
}
type cachedPlace struct {
	Location Location
	Name     string
	Expires  time.Time
}
type Routing struct {
	HTTP            *http.Client
	OSRM, Nominatim string
	mu              sync.Mutex
	routes          map[string]cachedRoute
	places          map[string]cachedPlace
	lastGeocode     time.Time
}

func (r *Routing) Geocode(ctx context.Context, query string) (Location, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(query))
	if p, ok := r.places[key]; ok && time.Now().Before(p.Expires) {
		return p.Location, p.Name, true
	}
	if !sleep(ctx, max(0, time.Second-time.Since(r.lastGeocode))) {
		return Location{}, "", false
	}
	r.lastGeocode = time.Now()
	endpoint := r.Nominatim
	if endpoint == "" {
		endpoint = "https://nominatim.openstreetmap.org/search"
	}
	var results []struct {
		Lat, Lon string
		Name     string `json:"display_name"`
	}
	err := getJSON(ctx, r.HTTP, endpoint+"?"+url.Values{"q": {query}, "format": {"json"}, "limit": {"1"}, "countrycodes": {"es"}}.Encode(), &results)
	if err != nil || len(results) == 0 {
		return Location{}, "", false
	}
	lat, e1 := strconv.ParseFloat(results[0].Lat, 64)
	lon, e2 := strconv.ParseFloat(results[0].Lon, 64)
	if e1 != nil || e2 != nil || !validLocation(lat, lon) {
		return Location{}, "", false
	}
	if len(r.places) >= 512 {
		clear(r.places)
	}
	if r.places == nil {
		r.places = map[string]cachedPlace{}
	}
	p := cachedPlace{Location{lat, lon}, truncate(results[0].Name, 200), time.Now().Add(24 * time.Hour)}
	r.places[key] = p
	return p.Location, p.Name, true
}
func (r *Routing) Routes(ctx context.Context, lat, lon float64, stations []Station) map[string]float64 {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]float64{}
	for _, s := range stations {
		key := fmt.Sprintf("%.5f,%.5f,%.5f,%.5f", lat, lon, s.Lat, s.Lon)
		if c, ok := r.routes[key]; ok && time.Now().Before(c.Expires) {
			out[s.ID] = c.KM
			continue
		}
		base := r.OSRM
		if base == "" {
			base = "https://router.project-osrm.org"
		}
		endpoint := fmt.Sprintf("%s/route/v1/driving/%f,%f;%f,%f?overview=false&alternatives=false&steps=false", base, lon, lat, s.Lon, s.Lat)
		var response struct {
			Routes []struct{ Distance float64 } `json:"routes"`
		}
		if err := getJSON(ctx, r.HTTP, endpoint, &response); err != nil || len(response.Routes) == 0 {
			break
		}
		km := response.Routes[0].Distance / 1000
		if km < 0 || math.IsNaN(km) || math.IsInf(km, 0) {
			continue
		}
		if len(r.routes) >= 2048 {
			clear(r.routes)
		}
		if r.routes == nil {
			r.routes = map[string]cachedRoute{}
		}
		r.routes[key] = cachedRoute{km, time.Now().Add(24 * time.Hour)}
		out[s.ID] = km
		if !sleep(ctx, 100*time.Millisecond) {
			break
		}
	}
	return out
}
