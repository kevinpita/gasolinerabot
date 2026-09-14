#!/usr/bin/env python3
"""Export preferences only. Pipe stdout directly to the Go importer, never to logs."""
import argparse
import json
import os
from pathlib import Path
import shutil
import sqlite3
import tempfile
import zipfile


def export(path, output):
    with sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True) as db:
        db.row_factory = sqlite3.Row
        rows = db.execute("SELECT * FROM user_prefs").fetchall()
    prefs = []
    defaults = {
        "consumption_l_100km": None,
        "alert_mode": "daily",
        "alert_hour": 8,
        "alert_weekday": None,
        "last_alert_at": None,
        "last_amount_type": None,
        "last_amount_value": None,
        "top_n": 5,
    }
    for row in rows:
        source = dict(row)
        item = {key: source[key] for key in ("chat_id", "lat", "lon", "radius_km")}
        item["fuels"] = json.loads(source["fuels_json"])
        item["alerts_enabled"] = bool(source["alerts_enabled"])
        for key, default in defaults.items():
            item[key] = source.get(key, default)
        prefs.append(item)
    json.dump(prefs, output, ensure_ascii=False, allow_nan=False)
    output.write("\n")


def main():
    import sys
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="Private SQLite file or original ZIP")
    args = parser.parse_args()
    os.umask(0o077)
    if zipfile.is_zipfile(args.source):
        # Copy only the database to a private temporary directory. Never extract .env.
        with zipfile.ZipFile(args.source) as archive:
            candidates = [i for i in archive.infolist() if i.filename.endswith("/data/gasolineras.sqlite3")]
            if len(candidates) != 1 or candidates[0].file_size > 1024**3:
                parser.error("Expected one SQLite database smaller than 1 GiB")
            with tempfile.TemporaryDirectory(prefix="gasolinerabot-import-") as directory:
                database = Path(directory) / "source.sqlite3"
                with archive.open(candidates[0]) as src, database.open("wb") as dst:
                    shutil.copyfileobj(src, dst)
                export(database, sys.stdout)
    else:
        export(args.source, sys.stdout)


if __name__ == "__main__":
    main()
