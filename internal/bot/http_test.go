package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The live Bot API rejects explicit JSON null with:
// Bad Request: object expected as reply markup.
// This applies to plain messages and the error notices sent after a failure.
func TestSendOmitsAbsentMarkup(t *testing.T) {
	for _, text := range []string{"Genial. ¿En qué radio busco?", "⚠️ No pude completar la consulta ahora mismo."} {
		t.Run(text, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Error(e)
					return
				}
				if raw, present := body["reply_markup"]; present {
					t.Errorf("message without buttons must omit reply_markup, got %s", raw)
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: object expected as reply markup"}`)
					return
				}
				_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
			}))
			defer server.Close()
			telegram := Telegram{HTTP: server.Client(), Base: server.URL}
			if e := telegram.Send(t.Context(), 42, text, nil); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestSendPreservesMarkup(t *testing.T) {
	for _, keys := range []*Markup{mainKeys(), locationKeys(), resultKeys(), {Remove: true}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]json.RawMessage
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			want, e := json.Marshal(keys)
			if e != nil {
				t.Error(e)
			}
			if string(body["reply_markup"]) != string(want) {
				t.Errorf("lost keyboard: %s", body["reply_markup"])
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
		}))
		telegram := Telegram{HTTP: server.Client(), Base: server.URL}
		if e := telegram.Send(t.Context(), 42, "buttons", keys); e != nil {
			t.Fatal(e)
		}
		server.Close()
	}
}

func TestTelegramDiagnosticCategories(t *testing.T) {
	for _, test := range []struct{ description, reason string }{
		{"Bad Request: object expected as reply markup", "invalid_reply_markup"},
		{"Bad Request: can't parse entities: private-message-text", "invalid_html"},
		{"Bad Request: query is too old and response timeout expired", "expired_callback"},
		{"unknown error containing secret-token", "unclassified"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": test.description + " secret-token"})
		}))
		telegram := Telegram{HTTP: server.Client(), Base: server.URL + "/secret-token"}
		err := telegram.Send(t.Context(), 42, "private-message-text", nil)
		if err == nil {
			t.Fatal("expected an error")
		}
		got := safeError(err)
		if !strings.Contains(got, "sendMessage HTTP 400 ("+test.reason+")") {
			t.Errorf("missing safe diagnostic: %s", got)
		}
		for _, secret := range []string{"secret-token", "private-message-text", server.URL} {
			if strings.Contains(got, secret) {
				t.Fatal("diagnostic leaked private data")
			}
		}
		server.Close()
	}
	if telegramMethod("secret-token") != "unknown_method" {
		t.Fatal("unknown methods must not enter logs")
	}
}
