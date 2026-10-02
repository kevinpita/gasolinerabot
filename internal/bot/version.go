package bot

import (
	"fmt"
	"time"
)

// versionMessage reports this binary's build and this process's lifetime.
func (a *App) versionMessage(now time.Time) string {
	version := a.Version
	if version == "" {
		version = "dev"
	}
	built := "desconocida"
	if t, err := time.Parse(time.RFC3339, a.BuildTime); err == nil {
		built = t.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	started, uptime := "desconocido", "desconocido"
	if !a.StartedAt.IsZero() {
		started = a.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC")
		elapsed := now.Sub(a.StartedAt)
		if elapsed < 0 {
			elapsed = 0
		}
		seconds := int64(elapsed / time.Second)
		uptime = fmt.Sprintf("%dd %02dh %02dm %02ds", seconds/86400, seconds/3600%24, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("⛽ <b>Versión del bot</b>\n\n🔖 <b>Commit:</b> <code>%s</code>\n🛠 <b>Compilación:</b> %s\n🚀 <b>Inicio del proceso:</b> %s\n⏱ <b>Tiempo activo:</b> %s", esc(version), built, started, uptime)
}
