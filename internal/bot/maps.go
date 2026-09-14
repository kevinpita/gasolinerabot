package bot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net/http"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type tileEntry struct {
	Image   image.Image
	Expires time.Time
}
type Maps struct {
	HTTP  *http.Client
	Tiles string
	mu    sync.Mutex
	cache map[string]tileEntry
}

var markerColors = []color.RGBA{{229, 57, 53, 255}, {30, 136, 229, 255}, {67, 160, 71, 255}, {251, 140, 0, 255}, {142, 36, 170, 255}, {0, 172, 193, 255}, {109, 76, 65, 255}, {57, 73, 171, 255}}

func project(lat, lon float64) (float64, float64) {
	lat = math.Max(-85.05, math.Min(85.05, lat))
	return (lon + 180) / 360, (1 - math.Asinh(math.Tan(lat*math.Pi/180))/math.Pi) / 2
}
func circlePoint(lat, lon, km, angle float64) (float64, float64) {
	p := math.Pi / 180
	a := km / 6371.0088
	b := angle * p
	phi := lat * p
	lam := lon * p
	phi2 := math.Asin(math.Sin(phi)*math.Cos(a) + math.Cos(phi)*math.Sin(a)*math.Cos(b))
	lam2 := lam + math.Atan2(math.Sin(b)*math.Sin(a)*math.Cos(phi), math.Cos(a)-math.Sin(phi)*math.Sin(phi2))
	return phi2 / p, lam2 / p
}
func line(img *image.RGBA, x1, y1, x2, y2 int, c color.Color) {
	dx, dy := float64(x2-x1), float64(y2-y1)
	steps := int(math.Max(math.Abs(dx), math.Abs(dy)))
	if steps == 0 {
		steps = 1
	}
	for i := 0; i <= steps; i++ {
		x := x1 + int(dx*float64(i)/float64(steps))
		y := y1 + int(dy*float64(i)/float64(steps))
		draw.Draw(img, image.Rect(x-1, y-1, x+2, y+2), &image.Uniform{c}, image.Point{}, draw.Src)
	}
}
func dot(img *image.RGBA, x, y, r int, c color.Color) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				img.Set(x+dx, y+dy, c)
			}
		}
	}
}
func (m *Maps) tile(ctx context.Context, z, x, y int) (image.Image, error) {
	n := 1 << z
	if y < 0 || y >= n {
		return nil, fmt.Errorf("tile out of range")
	}
	x = (x%n + n) % n
	base := m.Tiles
	if base == "" {
		base = "https://tile.openstreetmap.org"
	}
	u := fmt.Sprintf("%s/%d/%d/%d.png", base, z, x, y)
	if cached, ok := m.cache[u]; ok && time.Now().Before(cached.Expires) {
		return cached.Image, nil
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", userAgent)
	resp, e := m.HTTP.Do(req)
	if e != nil {
		return nil, fmt.Errorf("tile request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("tile unavailable")
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(data) > 1<<20 {
		return nil, fmt.Errorf("tile too large")
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil || cfg.Width != 256 || cfg.Height != 256 {
		return nil, fmt.Errorf("invalid tile")
	}
	img, e := png.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	if len(m.cache) >= 128 {
		clear(m.cache)
	}
	if m.cache == nil {
		m.cache = map[string]tileEntry{}
	}
	m.cache[u] = tileEntry{img, time.Now().Add(7 * 24 * time.Hour)}
	return img, nil
}

// Render creates an image in memory. User locations and map images never reach disk.
func (m *Maps) Render(ctx context.Context, lat, lon, radius float64, top []Ranking) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stations := []Station{}
	seen := map[string]bool{}
	for _, r := range top {
		for _, s := range r.Stations {
			if !seen[s.ID] && len(stations) < 8 {
				stations = append(stations, s)
				seen[s.ID] = true
			}
		}
	}
	points := [][2]float64{{lat, lon}}
	if top == nil {
		for a := 0.; a <= 360; a += 5 {
			la, lo := circlePoint(lat, lon, radius, a)
			points = append(points, [2]float64{la, lo})
		}
	} else {
		for _, s := range stations {
			points = append(points, [2]float64{s.Lat, s.Lon})
		}
	}
	minX, minY := 1., 1.
	maxX, maxY := 0., 0.
	for _, p := range points {
		x, y := project(p[0], p[1])
		minX = math.Min(minX, x)
		maxX = math.Max(maxX, x)
		minY = math.Min(minY, y)
		maxY = math.Max(maxY, y)
	}
	z := 15
	for z > 1 {
		scale := 256 * math.Exp2(float64(z))
		if (maxX-minX)*scale <= 800 && (maxY-minY)*scale <= 500 {
			break
		}
		z--
	}
	scale := 256 * math.Exp2(float64(z))
	left := (minX+maxX)/2*scale - 450
	upper := (minY+maxY)/2*scale - 325
	height := 650 + 60 + len(stations)*28
	img := image.NewRGBA(image.Rect(0, 0, 900, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{246, 247, 249, 255}}, image.Point{}, draw.Src)
	tiled := true
	for y := int(math.Floor(upper / 256)); y <= int(math.Floor((upper+649)/256)) && tiled; y++ {
		for x := int(math.Floor(left / 256)); x <= int(math.Floor((left+899)/256)); x++ {
			tile, e := m.tile(ctx, z, x, y)
			if e != nil {
				tiled = false
				break
			}
			rect := image.Rect(int(float64(x*256)-left), int(float64(y*256)-upper), int(float64(x*256)-left)+256, int(float64(y*256)-upper)+256)
			draw.Draw(img, rect.Intersect(image.Rect(0, 0, 900, 650)), tile, image.Point{max(0, -rect.Min.X), max(0, -rect.Min.Y)}, draw.Src)
		}
	}
	if !tiled {
		draw.Draw(img, image.Rect(0, 0, 900, 650), &image.Uniform{color.RGBA{246, 247, 249, 255}}, image.Point{}, draw.Src)
	}
	faceFont, e := opentype.Parse(goregular.TTF)
	if e != nil {
		return nil, e
	}
	face, e := opentype.NewFace(faceFont, &opentype.FaceOptions{Size: 18, DPI: 72, Hinting: font.HintingFull})
	if e != nil {
		return nil, e
	}
	defer face.Close()
	text := func(x, y int, s string, c color.Color) {
		d := font.Drawer{Dst: img, Src: &image.Uniform{c}, Face: face, Dot: fixed.P(x, y)}
		d.DrawString(s)
	}
	pixel := func(la, lo float64) (int, int) {
		x, y := project(la, lo)
		return int(x*scale - left), int(y*scale - upper)
	}
	blue := color.RGBA{25, 118, 210, 255}
	cx, cy := pixel(lat, lon)
	if top == nil {
		for i := 2; i < len(points); i++ {
			x1, y1 := pixel(points[i-1][0], points[i-1][1])
			x2, y2 := pixel(points[i][0], points[i][1])
			line(img, x1, y1, x2, y2, blue)
		}
	}
	for i, s := range stations {
		x, y := pixel(s.Lat, s.Lon)
		line(img, cx, cy, x, y, color.RGBA{100, 116, 139, 255})
		dot(img, x, y, 23, color.White)
		dot(img, x, y, 20, markerColors[i])
		text(x-6, y+6, fmt.Sprint(i+1), color.White)
	}
	dot(img, cx, cy, 10, blue)
	dot(img, cx, cy, 4, color.White)
	draw.Draw(img, image.Rect(0, 650, 900, height), &image.Uniform{color.White}, image.Point{}, draw.Src)
	label := "© OpenStreetMap contributors"
	if !tiled {
		label = "Mapa esquemático: no se pudieron cargar teselas OSM"
	}
	text(15, 675, label, color.Black)
	if top == nil {
		text(15, 702, fmt.Sprintf("Tu ubicación · radio %g km", radius), color.Black)
	} else {
		text(15, 702, "Leyenda", color.Black)
	}
	for i, s := range stations {
		text(15, 730+i*28, truncate(fmt.Sprintf("%d. %s · %.1f km · %s", i+1, s.Name, s.Distance, s.Town), 90), color.Black)
	}
	var buf bytes.Buffer
	e = png.Encode(&buf, img)
	return buf.Bytes(), e
}
