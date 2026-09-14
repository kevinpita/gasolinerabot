package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const userAgent = "gasolinerabot/1.0 (+https://github.com/kevinpita/gasolinerabot)"

func getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return fmt.Errorf("invalid upstream URL")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, e := c.Do(req)
	if e != nil {
		return fmt.Errorf("upstream request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Location struct {
	Lat float64 `json:"latitude"`
	Lon float64 `json:"longitude"`
}
type Message struct {
	ID       int64     `json:"message_id"`
	Chat     Chat      `json:"chat"`
	From     *Chat     `json:"from"`
	Text     string    `json:"text"`
	Location *Location `json:"location"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    Chat     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}
type Button struct {
	Text            string `json:"text"`
	Data            string `json:"callback_data,omitempty"`
	RequestLocation bool   `json:"request_location,omitempty"`
}
type Markup struct {
	Inline   [][]Button `json:"inline_keyboard,omitempty"`
	Keyboard [][]Button `json:"keyboard,omitempty"`
	Resize   bool       `json:"resize_keyboard,omitempty"`
	Once     bool       `json:"one_time_keyboard,omitempty"`
	Remove   bool       `json:"remove_keyboard,omitempty"`
}

func inline(rows ...[]Button) *Markup { return &Markup{Inline: rows} }
func button(text, data string) Button { return Button{Text: text, Data: data} }
func mainKeys() *Markup {
	return &Markup{Keyboard: [][]Button{{{Text: "⛽ Precios"}, {Text: "🚗 Repostar"}}, {{Text: "⏰ Alertas"}, {Text: "⚙️ Ajustes"}}}, Resize: true}
}
func locationKeys() *Markup {
	return &Markup{Keyboard: [][]Button{{{Text: "📍 Enviar mi ubicación", RequestLocation: true}}}, Resize: true, Once: true}
}
func resultKeys() *Markup {
	return inline([]Button{button("🗺 Ver en mapa", "map:prices")}, []Button{button("📈 Tendencia 7 días", "trend:prices")}, []Button{button("🚗 Repostar", "start:savings")})
}

type Telegram struct {
	HTTP *http.Client
	Base string
}
type telegramReply struct {
	OK         bool            `json:"ok"`
	Result     json.RawMessage `json:"result"`
	Code       int             `json:"error_code"`
	Parameters struct {
		Retry int `json:"retry_after"`
	} `json:"parameters"`
}
type TelegramError struct {
	Code  int
	Retry time.Duration
}

func (e *TelegramError) Error() string { return fmt.Sprintf("Telegram HTTP %d", e.Code) }
func (t *Telegram) request(ctx context.Context, method, contentType string, data []byte, out any) error {
	for attempt := 0; attempt < 3; attempt++ {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, t.Base+"/"+method, bytes.NewReader(data))
		if e != nil {
			return fmt.Errorf("invalid Telegram endpoint")
		}
		req.Header.Set("Content-Type", contentType)
		resp, e := t.HTTP.Do(req)
		if e != nil {
			return fmt.Errorf("Telegram request failed")
		}
		var reply telegramReply
		e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&reply)
		_ = resp.Body.Close()
		if e != nil {
			return fmt.Errorf("invalid Telegram response")
		}
		if !reply.OK {
			if reply.Code == 0 {
				reply.Code = resp.StatusCode
			}
			err := &TelegramError{reply.Code, time.Duration(reply.Parameters.Retry) * time.Second}
			if err.Code == 429 && err.Retry > 0 && err.Retry <= time.Minute && attempt < 2 {
				if !sleep(ctx, err.Retry) {
					return ctx.Err()
				}
				continue
			}
			return err
		}
		if out != nil {
			return json.Unmarshal(reply.Result, out)
		}
		return nil
	}
	return fmt.Errorf("Telegram retry limit")
}
func (t *Telegram) Call(ctx context.Context, method string, args any, out any) error {
	b, e := json.Marshal(args)
	if e != nil {
		return e
	}
	return t.request(ctx, method, "application/json", b, out)
}
func (t *Telegram) Send(ctx context.Context, id int64, text string, keys *Markup) error {
	return t.Call(ctx, "sendMessage", map[string]any{"chat_id": id, "text": text, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}, "reply_markup": keys}, nil)
}
func (t *Telegram) Photo(ctx context.Context, id int64, png []byte, caption string, keys *Markup) error {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range map[string]string{"chat_id": strconv.FormatInt(id, 10), "caption": caption} {
		if e := w.WriteField(k, v); e != nil {
			return e
		}
	}
	if keys != nil {
		m, e := json.Marshal(keys)
		if e != nil {
			return e
		}
		if e = w.WriteField("reply_markup", string(m)); e != nil {
			return e
		}
	}
	p, e := w.CreateFormFile("photo", "map.png")
	if e != nil {
		return e
	}
	if _, e = p.Write(png); e != nil {
		return e
	}
	if e = w.Close(); e != nil {
		return e
	}
	return t.request(ctx, "sendPhoto", w.FormDataContentType(), b.Bytes(), nil)
}
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func command(text string) (string, string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "", ""
	}
	name := strings.SplitN(parts[0], "@", 2)[0]
	return name, strings.TrimSpace(strings.TrimPrefix(text, parts[0]))
}
