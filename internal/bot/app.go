package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Session struct {
	State                   string
	Prefs                   Prefs
	Lat, Lon                float64
	Fuel, Kind, Destination string
	Amount, Consumption     float64
	Updated                 time.Time
}
type sessionKey struct{ Chat, User int64 }
type App struct {
	Store    *Store
	Telegram *Telegram
	Prices   *PriceSource
	Routing  *Routing
	Maps     *Maps
	Log      *slog.Logger
	sessions map[sessionKey]*Session
}

func safeError(err error) string {
	var operation *operationError
	if errors.As(err, &operation) {
		return operation.Error()
	}
	var te *TelegramError
	if errors.As(err, &te) {
		return te.Error()
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return classifiedError(err)
}
func (a *App) Run(ctx context.Context) error {
	a.sessions = map[sessionKey]*Session{}
	offset := int64(0)
	for ctx.Err() == nil {
		var updates []Update
		e := a.Telegram.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 30, "limit": 25, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		if e != nil {
			if ctx.Err() != nil {
				break
			}
			a.Log.Warn("poll failed", "error", safeError(e))
			if !sleep(ctx, 3*time.Second) {
				break
			}
			continue
		}
		for _, u := range updates {
			requestCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			e = a.Handle(requestCtx, u)
			cancel()
			if e != nil {
				a.Log.Error("update failed", "error", safeError(e))
				id := int64(0)
				if u.Message != nil {
					id = u.Message.Chat.ID
				} else if u.Callback != nil && u.Callback.Message != nil {
					id = u.Callback.Message.Chat.ID
				}
				if id != 0 {
					noticeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
					_ = a.Telegram.Send(noticeCtx, id, "⚠️ No pude completar la consulta ahora mismo. Prueba en unos minutos.", nil)
					stop()
				}
			}
			offset = u.ID + 1
			if ctx.Err() != nil {
				break
			}
		}
	}
	return ctx.Err()
}
func (a *App) Handle(ctx context.Context, u Update) error {
	m := u.Message
	data := ""
	user := int64(0)
	if u.Callback != nil {
		m = u.Callback.Message
		data = u.Callback.Data
		user = u.Callback.From.ID
		if e := a.Telegram.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": u.Callback.ID}, nil); e != nil {
			return e
		}
	}
	if m == nil {
		return nil
	}
	if m.From != nil && user == 0 {
		user = m.From.ID
	}
	id := m.Chat.ID
	key := sessionKey{id, user}
	if a.sessions == nil {
		a.sessions = map[sessionKey]*Session{}
	}
	for k, s := range a.sessions {
		if time.Since(s.Updated) > 30*time.Minute {
			delete(a.sessions, k)
		}
	}
	p, exists, e := a.Store.Get(ctx, id)
	if e != nil {
		return e
	}
	send := func(t string, k *Markup) error { return a.Telegram.Send(ctx, id, t, k) }
	text := strings.TrimSpace(m.Text)
	cmd, args := command(text)
	switch text {
	case "⛽ Precios":
		cmd = "/precios"
	case "🚗 Repostar":
		cmd = "/repostar"
	case "⏰ Alertas":
		cmd = "/alertas"
	case "⚙️ Ajustes":
		cmd = "/ajustes"
	}
	if data == "start:savings" {
		cmd = "/repostar"
		args = ""
	}
	if cmd == "/cancelar" {
		delete(a.sessions, key)
		return send("Cancelado.", &Markup{Remove: true})
	}
	startSession := func(s *Session) error {
		if len(a.sessions) >= 4096 && a.sessions[key] == nil {
			return fmt.Errorf("too many active conversations")
		}
		s.Updated = time.Now()
		a.sessions[key] = s
		return nil
	}
	switch cmd {
	case "/start", "/ajustes":
		// Start with saved settings, rather than resetting a user's alert schedule.
		if !exists {
			p.Fuels = []string{"gasolina95"}
		}
		if e = startSession(&Session{State: "location", Prefs: p}); e != nil {
			return e
		}
		return send("⛽ <b>Gasolineras baratas España</b>\n\nConfigura una ubicación base, combustible y radio.\n\nDespués queda simple:\n• <b>/precios</b> — ranking en tu zona\n• <b>/top 15</b> — elegir cuántas gasolineras salen\n• <b>/repostar</b> — calcula si compensa desplazarse\n• <b>/alertas</b> — avisos periódicos para esa zona\n\nPrimero, envíame la ubicación base.", locationKeys())
	case "/cerca":
		if e = startSession(&Session{State: "near", Prefs: p}); e != nil {
			return e
		}
		return send("📍 <b>Precios desde otra ubicación</b>\n\nEnvíame una ubicación y te saco el ranking cercano. No guardaré esa ubicación.", locationKeys())
	case "/repostar", "/ahorro":
		if e = startSession(&Session{State: "saving_location", Prefs: p, Destination: truncate(args, 200)}); e != nil {
			return e
		}
		extra := ""
		if args != "" {
			extra = "\n\n🎯 Usaré destino: <b>" + esc(truncate(args, 200)) + "</b>. Si no te lo encuentro, lo haré sin filtro de ruta."
		}
		return send("🚗 <b>Repostar</b>\n\nEnvíame la ubicación desde la que sales y calculo precio + desplazamiento."+extra, locationKeys())
	case "/precio", "/precios":
		if !exists {
			return send("Aún no tengo ubicación guardada. Usa <b>/start</b> para configurarla o envíame una ubicación y te saco precios para ese punto.", locationKeys())
		}
		return a.sendPrices(ctx, p, "", false)
	case "/top":
		if !exists {
			return send("Primero configura el bot con <b>/start</b>.", nil)
		}
		if args == "" {
			return send(fmt.Sprintf("📋 Ahora te muestro el <b>Top %d</b> por combustible.\n\nPara cambiarlo usa, por ejemplo, <code>/top 15</code> (entre 1 y 15).", p.Top), nil)
		}
		n, e := strconv.Atoi(args)
		if e != nil || n < 1 || n > 15 {
			return send("Indica un número entre 1 y 15. Ejemplo: <code>/top 15</code>", nil)
		}
		p.Top = n
		if e = a.Store.Save(ctx, p); e != nil {
			return e
		}
		return send(fmt.Sprintf("✅ Configurado: a partir de ahora te mostraré el <b>Top %d</b> por combustible.", n), nil)
	case "/tendencia":
		if !exists {
			return send("Aún no tengo tu configuración. Usa <b>/start</b> primero.", nil)
		}
		return a.trends(ctx, p)
	case "/avisos_on", "/avisos_off":
		if !exists {
			return send("Primero configura la ubicación de las alertas con <b>/start</b>.", nil)
		}
		p.Alerts = cmd == "/avisos_on"
		if e = a.Store.Save(ctx, p); e != nil {
			return e
		}
		if p.Alerts {
			return send("✅ Avisos activados.", nil)
		}
		return send("🔕 Avisos pausados. Puedes seguir usando <b>/precio</b>.", nil)
	case "/alertas", "/alertas_periodo":
		if !exists {
			return send("Primero configura el bot con <b>/start</b>.", nil)
		}
		if cmd == "/alertas_periodo" && args != "" {
			return a.setSchedule(ctx, p, args)
		}
		if e = startSession(&Session{State: "alerts", Prefs: p}); e != nil {
			return e
		}
		return send("⏰ <b>Configurar alertas</b>\n\nUsaré la ubicación/radio que tienes guardados. Puedes cambiarlos con <b>/ajustes</b>.\n\nAhora: <b>"+schedule(p)+"</b>\n\nElige una opción:", alertKeys())
	}
	if data == "map:prices" || data == "trend:prices" {
		if !exists {
			return send("Aún no tengo tu configuración. Usa <b>/start</b> primero.", nil)
		}
		if data == "trend:prices" {
			return a.trends(ctx, p)
		}
		snap, e := a.Prices.Fetch(ctx)
		if e != nil {
			return e
		}
		img, e := a.Maps.Render(ctx, p.Lat, p.Lon, p.Radius, rankings(snap.Stations, p))
		if e != nil {
			return e
		}
		short := []string{}
		for _, f := range p.Fuels {
			short = append(short, fuel(f).Short)
		}
		return a.Telegram.Photo(ctx, id, img, "🗺 Top gasolineras en mapa · "+strings.Join(short, ", ")+"\nDatos oficiales: "+snap.Updated, nil)
	}
	if s := a.sessions[key]; s != nil {
		s.Updated = time.Now()
		if strings.HasPrefix(cmd, "/") {
			return send("Usa <b>/cancelar</b> para salir de esta configuración.", nil)
		}
		return a.conversation(ctx, key, s, p, exists, m, data)
	}
	if m.Location != nil {
		if !validLocation(m.Location.Lat, m.Location.Lon) {
			return nil
		}
		p.Lat = m.Location.Lat
		p.Lon = m.Location.Lon
		if e = a.sendPrices(ctx, p, "ubicación enviada", false); e != nil {
			return e
		}
		return send("ℹ️ Ubicación usada solo para esta consulta, no se ha guardado.", &Markup{Remove: true})
	}
	if data != "" {
		return send("Esta opción ha caducado. Usa /start, /repostar o /alertas para continuar.", mainKeys())
	}
	return nil
}
func fuelKeys(selected []string) *Markup {
	rows := [][]Button{}
	for _, f := range Fuels {
		mark := "❌"
		for _, c := range selected {
			if f.Code == c {
				mark = "✅"
			}
		}
		rows = append(rows, []Button{button(mark+" "+f.Label, "fuel:"+f.Code)})
	}
	return inline(append(rows, []Button{button("Listo, continuar ➜", "fuel_done")})...)
}
func confirmKeys() *Markup {
	return inline([]Button{button("✅ Sí, usar este radio", "confirm_radius")}, []Button{button("↩️ Cambiar radio", "change_radius")}, []Button{button("📍 Cambiar ubicación", "change_location")})
}
func alertKeys() *Markup {
	return inline([]Button{button("📅 Diario · 08:00", "alerts_schedule:daily:8:")}, []Button{button("📅 Diario · 14:00", "alerts_schedule:daily:14:")}, []Button{button("📅 Diario · 20:00", "alerts_schedule:daily:20:")}, []Button{button("🗓 Semanal · lunes 09:00", "alerts_schedule:weekly:9:0")}, []Button{button("✍️ Personalizar", "alerts_schedule:custom")}, []Button{button("🔕 Pausar avisos", "alerts_schedule:off")})
}
func amountKeys(p Prefs) *Markup {
	rows := [][]Button{}
	if p.Amount != nil && p.AmountType != nil {
		rows = append(rows, []Button{button("✅ Repetir anterior: "+amountLabel(*p.AmountType, *p.Amount), fmt.Sprintf("saving_amount:%s:%g", *p.AmountType, *p.Amount))})
	}
	rows = append(rows, []Button{button("20 €", "saving_amount:euros:20"), button("30 €", "saving_amount:euros:30")}, []Button{button("50 €", "saving_amount:euros:50"), button("40 L", "saving_amount:liters:40")}, []Button{button("✍️ Otra cantidad", "saving_amount:custom")})
	return inline(rows...)
}
func consumptionKeys(p Prefs) *Markup {
	rows := [][]Button{}
	if p.Consumption != nil {
		rows = append(rows, []Button{button(fmt.Sprintf("✅ Usar guardado: %g L/100", *p.Consumption), fmt.Sprintf("saving_consumption:%g", *p.Consumption))})
	}
	rows = append(rows, []Button{button("5 L/100", "saving_consumption:5"), button("6 L/100", "saving_consumption:6")}, []Button{button("7 L/100", "saving_consumption:7"), button("8 L/100", "saving_consumption:8")}, []Button{button("✍️ Otro consumo", "saving_consumption:custom")})
	return inline(rows...)
}
func (a *App) conversation(ctx context.Context, key sessionKey, s *Session, current Prefs, exists bool, m *Message, data string) error {
	id := key.Chat
	send := func(t string, k *Markup) error { return a.Telegram.Send(ctx, id, t, k) }
	switch s.State {
	case "location", "near", "saving_location":
		if m.Location == nil || !validLocation(m.Location.Lat, m.Location.Lon) {
			return send("Necesito que me envíes una ubicación de Telegram.", locationKeys())
		}
		s.Lat = m.Location.Lat
		s.Lon = m.Location.Lon
		switch s.State {
		case "location":
			s.Prefs.Lat = s.Lat
			s.Prefs.Lon = s.Lon
			s.State = "fuels"
			if e := send("Perfecto. Ahora elige uno o varios combustibles:", &Markup{Remove: true}); e != nil {
				return e
			}
			return send("Combustibles:", fuelKeys(s.Prefs.Fuels))
		case "near":
			p := s.Prefs
			p.Lat = s.Lat
			p.Lon = s.Lon
			if e := a.sendPrices(ctx, p, "ubicación enviada", false); e != nil {
				return e
			}
			delete(a.sessions, key)
			return send("ℹ️ Ubicación usada solo para esta consulta, no se ha guardado.", &Markup{Remove: true})
		default:
			if len(s.Prefs.Fuels) == 1 {
				s.Fuel = s.Prefs.Fuels[0]
				s.State = "amount"
				return send("¿Cuánto vas a repostar?\nElige una opción rápida o escribe algo como <b>30 €</b> o <b>40 L</b>.", amountKeys(s.Prefs))
			}
			s.State = "saving_fuel"
			rows := [][]Button{}
			for _, c := range s.Prefs.Fuels {
				rows = append(rows, []Button{button("⛽ "+fuel(c).Label, "saving_fuel:"+c)})
			}
			if e := send("¿Para qué combustible lo calculo?", &Markup{Remove: true}); e != nil {
				return e
			}
			return send("Elige combustible:", inline(rows...))
		}
	case "fuels":
		if data == "fuel_done" {
			s.State = "radius"
			return send("Genial. ¿En qué radio busco?\n\nEjemplos: <b>20 km</b>, <b>100</b>, <b>200 kilómetros</b>", nil)
		}
		c := strings.TrimPrefix(data, "fuel:")
		if fuel(c).Code == "" {
			return nil
		}
		found := -1
		for i, f := range s.Prefs.Fuels {
			if c == f {
				found = i
			}
		}
		if found >= 0 {
			if len(s.Prefs.Fuels) == 1 {
				return send("Deja al menos un combustible seleccionado", nil)
			}
			s.Prefs.Fuels = append(s.Prefs.Fuels[:found], s.Prefs.Fuels[found+1:]...)
		} else {
			s.Prefs.Fuels = append(s.Prefs.Fuels, c)
		}
		return a.Telegram.Call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": id, "message_id": m.ID, "reply_markup": fuelKeys(s.Prefs.Fuels)}, nil)
	case "radius":
		n, ok := number(m.Text)
		if !ok || n <= 0 || n > 500 {
			return send("Pon un radio válido entre 1 y 500 km. Ejemplo: <b>100 km</b>", nil)
		}
		s.Prefs.Radius = n
		s.State = "confirm"
		if e := send("Te dibujo el radio para que confirmes si te encaja…", nil); e != nil {
			return e
		}
		img, e := a.Maps.Render(ctx, s.Prefs.Lat, s.Prefs.Lon, n, nil)
		if e != nil {
			return send(fmt.Sprintf("No pude generar la imagen del mapa, pero tengo guardado el radio de <b>%g km</b>. ¿Lo usamos?", n), confirmKeys())
		}
		return a.Telegram.Photo(ctx, id, img, fmt.Sprintf("🗺 Radio elegido: %g km\n\n¿Estás de acuerdo con esta zona de búsqueda?", n), confirmKeys())
	case "confirm":
		if data == "change_radius" {
			s.State = "radius"
			return send("Sin problema. Dime otro radio en km. Ejemplo: <b>50 km</b>", nil)
		}
		if data == "change_location" {
			s.State = "location"
			return send("Vale, envíame otra ubicación.", locationKeys())
		}
		if data != "confirm_radius" {
			return nil
		}
		p := s.Prefs
		if exists {
			p = current
			p.Lat = s.Prefs.Lat
			p.Lon = s.Prefs.Lon
			p.Radius = s.Prefs.Radius
			p.Fuels = s.Prefs.Fuels
		}
		if e := a.Store.Save(ctx, p); e != nil {
			return e
		}
		delete(a.sessions, key)
		if e := send("✅ Configurado. Te mando los precios ahora.\n\nComandos útiles:\n• <b>/precios</b> — ver la lista para esta ubicación\n• <b>/top 15</b> — elegir cuántas gasolineras salen\n• Enviar una <b>ubicación</b> — ver precios cerca de ese punto\n• <b>/repostar</b> — ver si compensa ir a otra gasolinera\n• <b>/alertas</b> — avisos periódicos para esta ubicación\n• <b>/ajustes</b> — cambiar ubicación/radio/combustibles\n• <b>/avisos_on</b> / <b>/avisos_off</b> — activar o pausar avisos", mainKeys()); e != nil {
			return e
		}
		return a.sendPrices(ctx, p, "", false)
	case "saving_fuel":
		c := strings.TrimPrefix(data, "saving_fuel:")
		valid := false
		for _, f := range s.Prefs.Fuels {
			if f == c {
				valid = true
			}
		}
		if !valid {
			return nil
		}
		s.Fuel = c
		s.State = "amount"
		return send("¿Cuánto vas a repostar?\nElige una opción rápida o escribe algo como <b>30 €</b> o <b>40 L</b>.", amountKeys(s.Prefs))
	case "amount":
		if data == "saving_amount:custom" {
			return send("Escríbeme la cantidad. Ejemplos: <b>30 €</b>, <b>50 euros</b>, <b>40 L</b>.", nil)
		}
		kind, v, ok := amount(m.Text)
		if strings.HasPrefix(data, "saving_amount:") {
			parts := strings.Split(data, ":")
			if len(parts) != 3 {
				return nil
			}
			kind = parts[1]
			var e error
			v, e = strconv.ParseFloat(parts[2], 64)
			ok = e == nil && (kind == "euros" || kind == "liters") && v > 0 && v <= 10000
		}
		if !ok {
			return send("No entendí la cantidad. Prueba con <b>30 €</b> o <b>40 L</b>.", nil)
		}
		s.Kind = kind
		s.Amount = v
		s.State = "consumption"
		return send("¿Cuánto consume el coche aproximadamente?\n\nLo usaré para estimar el coste del desplazamiento.", consumptionKeys(s.Prefs))
	case "consumption":
		if data == "saving_consumption:custom" {
			return send("Escríbeme el consumo medio. Ejemplo: <b>6,5 L/100</b>.", nil)
		}
		text := m.Text
		if strings.HasPrefix(data, "saving_consumption:") {
			text = strings.TrimPrefix(data, "saving_consumption:")
		}
		v, ok := number(text)
		if !ok || v < 2 || v > 25 {
			return send("Pon un consumo razonable, por ejemplo <b>6,5 L/100</b>.", nil)
		}
		s.Consumption = v
		if exists {
			current.Consumption = &v
			current.AmountType = &s.Kind
			current.Amount = &s.Amount
			if e := a.Store.Save(ctx, current); e != nil {
				return e
			}
		}
		if e := a.finishSavings(ctx, id, s); e != nil {
			return e
		}
		delete(a.sessions, key)
		return nil
	case "alerts":
		if !exists {
			return send("Primero configura el bot con <b>/start</b>.", nil)
		}
		if data == "alerts_schedule:off" {
			current.Alerts = false
			if e := a.Store.Save(ctx, current); e != nil {
				return e
			}
			delete(a.sessions, key)
			return send("🔕 Avisos pausados. Puedes reactivarlos con <b>/alertas</b> o <b>/avisos_on</b>.", nil)
		}
		if data == "alerts_schedule:custom" {
			return send("✍️ Escríbeme cuándo quieres el aviso:\n\n• <code>diario 9</code>\n• <code>diario 20</code>\n• <code>semanal lunes 9</code>\n• <code>semanal viernes 18</code>", nil)
		}
		text := m.Text
		if strings.HasPrefix(data, "alerts_schedule:") {
			parts := strings.Split(data, ":")
			if len(parts) != 4 {
				return nil
			}
			if parts[1] == "daily" {
				text = "diario " + parts[2]
			} else if parts[1] == "weekly" {
				d, e := strconv.Atoi(parts[3])
				if e != nil || d < 0 || d > 6 {
					return nil
				}
				text = "semanal " + weekdays[d] + " " + parts[2]
			} else {
				return nil
			}
		}
		if _, _, _, ok := parseSchedule(text); !ok {
			return send("No lo entendí. Prueba con <code>diario 9</code> o <code>semanal lunes 9</code>.", nil)
		}
		if e := a.setSchedule(ctx, current, text); e != nil {
			return e
		}
		delete(a.sessions, key)
		return nil
	}
	return nil
}
func (a *App) setSchedule(ctx context.Context, p Prefs, text string) error {
	mode, h, d, ok := parseSchedule(text)
	if !ok {
		return a.Telegram.Send(ctx, p.ChatID, "No lo entendí. Ejemplos:\n• <code>/alertas_periodo diario 9</code>\n• <code>/alertas_periodo semanal lunes 9</code>", nil)
	}
	p.Mode = mode
	p.Hour = h
	p.Weekday = d
	p.Alerts = true
	if e := a.Store.Save(ctx, p); e != nil {
		return e
	}
	return a.Telegram.Send(ctx, p.ChatID, "✅ Alertas configuradas: <b>"+schedule(p)+"</b>", nil)
}
func (a *App) sendPrices(ctx context.Context, p Prefs, label string, alert bool) error {
	snap, e := a.Prices.Fetch(ctx)
	if e != nil {
		return e
	}
	messages := digest(rankings(snap.Stations, p), p.Radius, snap.Updated, label)
	for i, text := range messages {
		if alert && i == 0 {
			text = "🌅 <b>Informe diario</b>\n\n" + text
		}
		var keys *Markup
		if label == "" && i == len(messages)-1 {
			keys = resultKeys()
		}
		if e = a.Telegram.Send(ctx, p.ChatID, text, keys); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) finishSavings(ctx context.Context, id int64, s *Session) error {
	snap, e := a.Prices.Fetch(ctx)
	if e != nil {
		return e
	}
	candidates := nearby(snap.Stations, s.Lat, s.Lon, s.Prefs.Radius, s.Fuel)
	if s.Destination != "" {
		loc, name, ok := a.Routing.Geocode(ctx, s.Destination)
		notice := ""
		if !ok {
			notice = "⚠️ No pude localizar <b>" + esc(s.Destination) + "</b>; te muestro opciones sin filtrar por ruta."
		} else {
			filtered := along(candidates, s.Lat, s.Lon, loc.Lat, loc.Lon)
			if len(filtered) > 0 {
				candidates = filtered
				notice = "🧭 Aplicando dirección hacia <b>" + esc(strings.Split(name, ",")[0]) + "</b> (filtro aproximado)."
			} else {
				notice = "ℹ️ No encontré gasolineras en esa dirección para ese radio. Te muestro opciones sin filtrar de ruta."
			}
		}
		if e = a.Telegram.Send(ctx, id, notice, nil); e != nil {
			return e
		}
	}
	if len(candidates) == 0 {
		return a.Telegram.Send(ctx, id, "No encontré gasolineras con ese combustible dentro de tu radio configurado.", mainKeys())
	}
	targets := append([]Station(nil), candidates...)
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].Prices[s.Fuel] < targets[j].Prices[s.Fuel] })
	if len(targets) > 10 {
		targets = targets[:10]
	}
	found := false
	for _, st := range targets {
		if st.ID == candidates[0].ID {
			found = true
		}
	}
	if !found {
		targets = append(targets, candidates[0])
	}
	routes := a.Routing.Routes(ctx, s.Lat, s.Lon, targets)
	for _, mode := range []string{"one_way", "round_trip"} {
		analysis := analyze(candidates, s.Fuel, s.Kind, s.Amount, s.Consumption, routes, mode, snap.Updated)
		for _, text := range chunks("", strings.SplitAfter(savingsMessage(analysis), "\n"), "") {
			if e = a.Telegram.Send(ctx, id, text, nil); e != nil {
				return e
			}
		}
	}
	return a.Telegram.Send(ctx, id, "✅ Listo. Si quieres otra ubicación, vuelve a pedir <b>/repostar</b>.", mainKeys())
}
