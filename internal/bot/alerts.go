package bot

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	_ "time/tzdata"
)

var madrid = func() *time.Location {
	z, e := time.LoadLocation("Europe/Madrid")
	if e != nil {
		panic(e)
	}
	return z
}()
var weekdays = []string{"lunes", "martes", "miércoles", "jueves", "viernes", "sábado", "domingo"}

func parseSchedule(text string) (string, int, *int, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	n, ok := number(text)
	if !ok || n != math.Trunc(n) || n < 0 || n > 23 {
		return "", 0, nil, false
	}
	// The UI supports whole hours only. Do not silently discard requested minutes.
	if strings.Contains(text, ":") {
		parts := strings.SplitN(text, ":", 2)
		if !strings.HasPrefix(parts[1], "00") {
			return "", 0, nil, false
		}
	}
	if strings.Contains(text, "sem") {
		for i, day := range weekdays {
			plain := strings.NewReplacer("é", "e", "á", "a").Replace(day)
			if strings.Contains(text, day) || strings.Contains(text, plain) {
				return "weekly", int(n), &i, true
			}
		}
		return "", 0, nil, false
	}
	return "daily", int(n), nil, true
}
func schedule(p Prefs) string {
	if p.Mode == "weekly" && p.Weekday != nil {
		return fmt.Sprintf("semanal · %s · %02d:00", weekdays[*p.Weekday], p.Hour)
	}
	return fmt.Sprintf("diario · %02d:00", p.Hour)
}
func due(now time.Time, p Prefs) bool {
	now = now.In(madrid)
	if !p.Alerts || now.Hour() != p.Hour {
		return false
	}
	day := (int(now.Weekday()) + 6) % 7
	if p.Mode == "weekly" && (p.Weekday == nil || *p.Weekday != day) {
		return false
	}
	if p.LastAlert == nil {
		return true
	}
	last := p.LastAlert.In(madrid)
	if p.Mode == "weekly" {
		a, b := now.ISOWeek()
		c, d := last.ISOWeek()
		return a != c || b != d
	}
	return now.Format("2006-01-02") != last.Format("2006-01-02")
}
func (a *App) CheckAlerts(ctx context.Context) error {
	prefs, e := a.Store.Alerts(ctx)
	if e != nil {
		return e
	}
	now := time.Now()
	for _, p := range prefs {
		if !due(now, p) {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		e = a.sendPrices(sendCtx, p, "", true)
		if e == nil {
			e = a.Store.MarkAlert(sendCtx, p.ChatID, now)
		}
		cancel()
		if e != nil {
			a.Log.Error("scheduled alert failed", "error", safeError(e))
		}
		if !sleep(ctx, 100*time.Millisecond) {
			return ctx.Err()
		}
	}
	return nil
}
func (a *App) trends(ctx context.Context, p Prefs) error {
	snap, e := a.Prices.Fetch(ctx)
	if e != nil {
		return e
	}
	top := rankings(snap.Stations, p)
	blocks := []string{}
	has := false
	for _, r := range top {
		heading := "\n\n<b>▸ " + esc(fuel(r.Fuel).Label) + "</b>"
		if len(r.Stations) == 0 {
			blocks = append(blocks, heading+"\n   Sin precios en este radio.")
			continue
		}
		for i, s := range r.Stations {
			if i == 5 {
				break
			}
			h, e := a.Store.History(ctx, s.ID, r.Fuel, time.Now())
			if e != nil {
				return e
			}
			prefix := ""
			if i == 0 {
				prefix = heading
			}
			line := fmt.Sprintf("\n• <b>%s</b> · %s · %.3f €/L · ", esc(s.Name), esc(place(s)), s.Prices[r.Fuel])
			if len(h) < 2 {
				blocks = append(blocks, prefix+line+"histórico insuficiente")
				continue
			}
			has = true
			delta := h[len(h)-1].Price - h[0].Price
			label := "➖ estable"
			if delta > 0.001 {
				label = fmt.Sprintf("↗️ sube +%.3f €/L", delta)
			} else if delta < -0.001 {
				label = fmt.Sprintf("↘️ baja %.3f €/L", delta)
			}
			blocks = append(blocks, prefix+line+label+"\n  "+sparkline(h)+" · "+h[0].Date.Format("01-02")+" → "+h[len(h)-1].Date.Format("01-02"))
		}
	}
	footer := ""
	if !has {
		footer = "\n\nℹ️ Aún estoy empezando a guardar histórico. Tendrás tendencia real cuando haya al menos 2 días de datos."
	}
	for _, text := range chunks("📈 <b>Tendencia últimos 7 días</b>\n🕒 Datos actuales: "+esc(snap.Updated), blocks, footer) {
		if e = a.Telegram.Send(ctx, p.ChatID, text, nil); e != nil {
			return e
		}
	}
	return nil
}
func sparkline(h []HistoryPoint) string {
	lo, hi := h[0].Price, h[0].Price
	for _, p := range h {
		lo = math.Min(lo, p.Price)
		hi = math.Max(hi, p.Price)
	}
	ticks := []rune("▁▂▃▄▅▆▇█")
	out := []rune{}
	for _, p := range h {
		n := 0
		if hi != lo {
			n = int(math.RoundToEven((p.Price - lo) / (hi - lo) * 7))
		}
		out = append(out, ticks[n])
	}
	return string(out)
}
