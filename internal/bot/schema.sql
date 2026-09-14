CREATE TABLE IF NOT EXISTS user_prefs (
 chat_id bigint PRIMARY KEY,
 lat double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
 lon double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
 radius_km double precision NOT NULL CHECK (radius_km > 0 AND radius_km <= 500),
 fuels text[] NOT NULL CHECK (cardinality(fuels) > 0 AND fuels <@ ARRAY['gasolina95','gasolina98','diesel','diesel_premium','glp']::text[]),
 alerts_enabled boolean NOT NULL DEFAULT true,
 consumption_l_100km double precision CHECK (consumption_l_100km BETWEEN 2 AND 25),
 alert_mode text NOT NULL DEFAULT 'daily' CHECK (alert_mode IN ('daily','weekly')),
 alert_hour integer NOT NULL DEFAULT 8 CHECK (alert_hour BETWEEN 0 AND 23),
 alert_weekday integer CHECK (alert_weekday BETWEEN 0 AND 6),
 last_alert_at timestamptz,
 last_amount_type text CHECK (last_amount_type IN ('euros','liters')),
 last_amount_value double precision CHECK (last_amount_value > 0 AND last_amount_value <= 10000),
 top_n integer NOT NULL DEFAULT 5 CHECK (top_n BETWEEN 1 AND 15),
 CHECK (alert_mode != 'weekly' OR alert_weekday IS NOT NULL)
);
CREATE TABLE IF NOT EXISTS price_history (
 station_id text NOT NULL,
 fuel_code text NOT NULL,
 observed_date date NOT NULL,
 price numeric(6,3) NOT NULL CHECK (price > 0),
 PRIMARY KEY (station_id, fuel_code, observed_date)
);
CREATE INDEX IF NOT EXISTS price_history_date_idx ON price_history (observed_date);
