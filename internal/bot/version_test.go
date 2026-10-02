package bot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Protect the deployment fields, UTC output, and uptime across day boundaries.
func TestVersionMessage(t *testing.T) {
	started := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	a := App{Version: "abc123<&>", BuildTime: "2026-04-01T13:30:00+02:00", StartedAt: started}
	got := a.versionMessage(started.Add(49*time.Hour + 3*time.Minute + 4*time.Second + 500*time.Millisecond))
	for _, want := range []string{
		"<b>Versión del bot</b>",
		"<code>abc123&lt;&amp;&gt;</code>",
		"2026-04-01 11:30:00 UTC",
		"2026-04-01 12:00:00 UTC",
		"2d 01h 03m 04s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q: %s", want, got)
		}
	}
}

// Missing metadata must not display a zero date or a false uptime.
func TestVersionMessageFallbacks(t *testing.T) {
	for _, built := range []string{"", "invalid"} {
		a := App{BuildTime: built}
		got := a.versionMessage(time.Now())
		for _, want := range []string{"<code>dev</code>", "Compilación:</b> desconocida", "Inicio del proceso:</b> desconocido", "Tiempo activo:</b> desconocido"} {
			if !strings.Contains(got, want) {
				t.Errorf("message missing %q: %s", want, got)
			}
		}
	}
	started := time.Now()
	a := App{StartedAt: started}
	if got := a.versionMessage(started.Add(-time.Second)); !strings.Contains(got, "0d 00h 00m 00s") {
		t.Errorf("negative uptime: %s", got)
	}
}

// /version must work without a database or preferences and leave a flow intact.
func TestHandleVersion(t *testing.T) {
	for _, command := range []string{"/version", " /version@gasolinerabot "} {
		t.Run(command, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body struct {
					ChatID    int64  `json:"chat_id"`
					Text      string `json:"text"`
					ParseMode string `json:"parse_mode"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/sendMessage" || body.ChatID != 42 || body.ParseMode != "HTML" || !strings.Contains(body.Text, "<code>deadbeef</code>") {
					t.Errorf("unexpected version reply: %s %+v", r.URL.Path, body)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
			}))
			defer server.Close()
			key := sessionKey{Chat: 42, User: 7}
			session := &Session{State: "location", Updated: time.Now()}
			a := App{
				Telegram: &Telegram{HTTP: server.Client(), Base: server.URL},
				Version:  "deadbeef",
				sessions: map[sessionKey]*Session{key: session},
			}
			if err := a.Handle(t.Context(), Update{Message: &Message{Chat: Chat{ID: 42}, From: &Chat{ID: 7}, Text: command}}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || a.sessions[key] != session {
				t.Fatal("version command must send one reply and preserve the active flow")
			}
		})
	}
}
