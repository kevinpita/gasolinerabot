import importlib.util
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("exporter", Path(__file__).with_name("export-preferences.py"))
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class ExportTests(unittest.TestCase):
    def test_preferences_only(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "source.sqlite3"
            with sqlite3.connect(path) as db:
                db.execute("CREATE TABLE user_prefs(chat_id INTEGER,lat REAL,lon REAL,radius_km REAL,fuels_json TEXT,alerts_enabled INTEGER,last_digest_key TEXT)")
                db.execute("INSERT INTO user_prefs VALUES(1,40,-3,20,?,0,'old-digest')", ('["gasolina95"]',))
                db.execute("CREATE TABLE price_history(price REAL)")
                db.execute("INSERT INTO price_history VALUES(1.5)")
            output = io.StringIO()
            exporter.export(path, output)
            prefs = json.loads(output.getvalue())
            self.assertEqual(len(prefs), 1)
            self.assertFalse(prefs[0]["alerts_enabled"])
            self.assertEqual(prefs[0]["top_n"], 5)
            self.assertNotIn("price_history", output.getvalue())
            self.assertNotIn("last_digest_key", prefs[0])
            with sqlite3.connect(path) as db:
                self.assertEqual(db.execute("SELECT count(*) FROM price_history").fetchone()[0], 1)


if __name__ == "__main__":
    unittest.main()
