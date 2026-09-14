package bot

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type Prefs struct {
	ChatID      int64      `json:"chat_id"`
	Lat         float64    `json:"lat"`
	Lon         float64    `json:"lon"`
	Radius      float64    `json:"radius_km"`
	Fuels       []string   `json:"fuels"`
	Alerts      bool       `json:"alerts_enabled"`
	Consumption *float64   `json:"consumption_l_100km"`
	Mode        string     `json:"alert_mode"`
	Hour        int        `json:"alert_hour"`
	Weekday     *int       `json:"alert_weekday"`
	LastAlert   *time.Time `json:"last_alert_at"`
	AmountType  *string    `json:"last_amount_type"`
	Amount      *float64   `json:"last_amount_value"`
	Top         int        `json:"top_n"`
}

func defaults(id int64) Prefs {
	return Prefs{ChatID: id, Radius: 20, Fuels: []string{"gasolina95", "diesel"}, Alerts: true, Mode: "daily", Hour: 8, Top: 5}
}

type Store struct{ Pool *pgxpool.Pool }

func OpenStore(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("invalid DATABASE_URL")
	}
	cfg.MaxConns = 5
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot create database pool")
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database connection failed")
	}
	s := &Store{pool}
	if err = s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) migrate(ctx context.Context) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(7142051)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY)"); e != nil {
		return e
	}
	var done bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=1)").Scan(&done); e != nil {
		return e
	}
	if !done {
		if _, e = tx.Exec(ctx, schema); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations VALUES(1)"); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

const columns = "chat_id,lat,lon,radius_km,fuels,alerts_enabled,consumption_l_100km,alert_mode,alert_hour,alert_weekday,last_alert_at,last_amount_type,last_amount_value,top_n"

func scanPrefs(row pgx.Row) (Prefs, error) {
	var p Prefs
	err := row.Scan(&p.ChatID, &p.Lat, &p.Lon, &p.Radius, &p.Fuels, &p.Alerts, &p.Consumption, &p.Mode, &p.Hour, &p.Weekday, &p.LastAlert, &p.AmountType, &p.Amount, &p.Top)
	return p, err
}
func (s *Store) Get(ctx context.Context, id int64) (Prefs, bool, error) {
	p, e := scanPrefs(s.Pool.QueryRow(ctx, "SELECT "+columns+" FROM user_prefs WHERE chat_id=$1", id))
	if errors.Is(e, pgx.ErrNoRows) {
		return defaults(id), false, nil
	}
	return p, e == nil, e
}
func prefsArgs(p Prefs) []any {
	return []any{p.ChatID, p.Lat, p.Lon, p.Radius, p.Fuels, p.Alerts, p.Consumption, p.Mode, p.Hour, p.Weekday, p.LastAlert, p.AmountType, p.Amount, p.Top}
}

const insertPrefs = "INSERT INTO user_prefs(" + columns + ") VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)"

func (s *Store) Save(ctx context.Context, p Prefs) error {
	_, e := s.Pool.Exec(ctx, insertPrefs+` ON CONFLICT(chat_id) DO UPDATE SET
 lat=EXCLUDED.lat,lon=EXCLUDED.lon,radius_km=EXCLUDED.radius_km,fuels=EXCLUDED.fuels,
 alerts_enabled=EXCLUDED.alerts_enabled,consumption_l_100km=EXCLUDED.consumption_l_100km,
 alert_mode=EXCLUDED.alert_mode,alert_hour=EXCLUDED.alert_hour,alert_weekday=EXCLUDED.alert_weekday,
 last_amount_type=EXCLUDED.last_amount_type,last_amount_value=EXCLUDED.last_amount_value,top_n=EXCLUDED.top_n`, prefsArgs(p)...)
	return e
}
func (s *Store) Alerts(ctx context.Context) ([]Prefs, error) {
	rows, e := s.Pool.Query(ctx, "SELECT "+columns+" FROM user_prefs WHERE alerts_enabled")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Prefs{}
	for rows.Next() {
		p, e := scanPrefs(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) MarkAlert(ctx context.Context, id int64, t time.Time) error {
	_, e := s.Pool.Exec(ctx, "UPDATE user_prefs SET last_alert_at=$2 WHERE chat_id=$1", id, t)
	return e
}
func (s *Store) Record(ctx context.Context, snap Snapshot) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, e = tx.Exec(ctx, "CREATE TEMP TABLE snapshot (LIKE price_history INCLUDING DEFAULTS) ON COMMIT DROP"); e != nil {
		return e
	}
	rows := [][]any{}
	for _, s := range snap.Stations {
		for f, p := range s.Prices {
			rows = append(rows, []any{s.ID, f, snap.Date.Format("2006-01-02"), fmt.Sprintf("%.3f", p)})
		}
	}
	// Text-to-numeric conversion is handled by PostgreSQL during COPY via explicit typed values below.
	_, e = tx.CopyFrom(ctx, pgx.Identifier{"snapshot"}, []string{"station_id", "fuel_code", "observed_date", "price"}, pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		date, e := time.Parse("2006-01-02", r[2].(string))
		if e != nil {
			return nil, e
		}
		r[2] = date
		// pgx numeric accepts a Numeric value, not a formatted string in binary COPY.
		var price pgtype.Numeric
		if e = price.Scan(r[3]); e != nil {
			return nil, e
		}
		r[3] = price
		return r, nil
	}))
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO price_history SELECT DISTINCT ON(station_id,fuel_code,observed_date) * FROM snapshot ON CONFLICT(station_id,fuel_code,observed_date) DO UPDATE SET price=EXCLUDED.price`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "DELETE FROM price_history WHERE observed_date < $1::date - 30", snap.Date); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type HistoryPoint struct {
	Date  time.Time
	Price float64
}

func (s *Store) History(ctx context.Context, id, code string, now time.Time) ([]HistoryPoint, error) {
	rows, e := s.Pool.Query(ctx, "SELECT observed_date,price::double precision FROM price_history WHERE station_id=$1 AND fuel_code=$2 AND observed_date >= $3::date - 6 ORDER BY observed_date", id, code, now.In(madrid))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []HistoryPoint{}
	for rows.Next() {
		var p HistoryPoint
		if e = rows.Scan(&p.Date, &p.Price); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ImportPrefs inserts preferences only, in one transaction, and never overwrites an existing chat.
func (s *Store) ImportPrefs(ctx context.Context, r io.Reader) (int, error) {
	var prefs []Prefs
	d := json.NewDecoder(io.LimitReader(r, 16<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(&prefs); e != nil {
		return 0, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return 0, fmt.Errorf("unexpected data after preferences")
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	count := 0
	for _, p := range prefs {
		p.Fuels = normalizeFuels(p.Fuels)
		tag, e := tx.Exec(ctx, insertPrefs+" ON CONFLICT(chat_id) DO NOTHING", prefsArgs(p)...)
		if e != nil {
			return 0, fmt.Errorf("invalid preferences, import rolled back")
		}
		count += int(tag.RowsAffected())
	}
	return count, tx.Commit(ctx)
}
