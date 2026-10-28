"""Tests for import-dashboard.py against a fake Grafana (no real server touched).

Run from the repo root:  python3 -m unittest discover -s grafana -v
"""
import base64
import json
import os
import subprocess
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer

SCRIPT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "import-dashboard.py")


class Fake(BaseHTTPRequestHandler):
    routes = {}  # (method, path) -> (status, body); set per test
    log = []     # (method, path, headers, parsed body)

    def _handle(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n)) if n else None
        type(self).log.append((self.command, self.path, dict(self.headers), body))
        status, payload = type(self).routes.get((self.command, self.path), (404, {"message": "not found"}))
        data = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = do_POST = _handle

    def log_message(self, *a):
        pass


class ImportDashboardTest(unittest.TestCase):
    def setUp(self):
        Fake.routes = {("POST", "/api/dashboards/db"): (200, {"url": "/d/information-broker-ops/x", "status": "success"})}
        Fake.log = []
        self.server = HTTPServer(("127.0.0.1", 0), Fake)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.url = "http://127.0.0.1:%d" % self.server.server_port

    def tearDown(self):
        self.server.shutdown()

    def run_script(self, **env):
        e = {"PATH": os.environ.get("PATH", ""), "GRAFANA_URL": self.url}
        e.update(env)
        return subprocess.run([sys.executable, SCRIPT], env=e, capture_output=True, text=True, timeout=30)

    def requests(self, method, path):
        return [r for r in Fake.log if r[0] == method and r[1] == path]

    def test_basic_auth_header_from_user_and_password(self):
        r = self.run_script(GRAFANA_USER="alice", GRAFANA_PASSWORD="s3cret")
        self.assertEqual(r.returncode, 0, r.stderr)
        want = "Basic " + base64.b64encode(b"alice:s3cret").decode()
        self.assertEqual(self.requests("POST", "/api/dashboards/db")[0][2].get("Authorization"), want)
        self.assertNotIn("s3cret", r.stdout + r.stderr, "the password must never be printed")

    def test_token_wins_over_user_and_password(self):
        r = self.run_script(GRAFANA_TOKEN="tok123", GRAFANA_USER="alice", GRAFANA_PASSWORD="s3cret")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.requests("POST", "/api/dashboards/db")[0][2].get("Authorization"), "Bearer tok123")

    def test_no_authorization_header_when_nothing_configured(self):
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("Authorization", self.requests("POST", "/api/dashboards/db")[0][2])

    def test_existing_data_source_by_name_is_preselected(self):
        Fake.routes[("GET", "/api/datasources/name/Prometheus")] = (200, {"uid": "abc123", "url": "http://prometheus:9090"})
        r = self.run_script(DATASOURCE_NAME="Prometheus")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.requests("POST", "/api/datasources"), [], "must not create a data source in lookup mode")
        dash = self.requests("POST", "/api/dashboards/db")[0][3]["dashboard"]
        ds = [v for v in dash["templating"]["list"] if v["name"] == "ds"][0]
        self.assertEqual(ds["current"]["value"], "abc123")
        self.assertEqual(ds["current"]["text"], "Prometheus")

    def test_missing_data_source_without_prometheus_url_fails_before_importing(self):
        r = self.run_script(DATASOURCE_NAME="Nope")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("not found", r.stderr)
        self.assertEqual(self.requests("POST", "/api/dashboards/db"), [], "nothing may be imported when the data source is missing")

    def test_prometheus_url_still_creates_the_data_source(self):
        Fake.routes[("POST", "/api/datasources")] = (200, {"datasource": {"uid": "new1"}})
        r = self.run_script(PROMETHEUS_URL="http://prom:9090")
        self.assertEqual(r.returncode, 0, r.stderr)
        made = self.requests("POST", "/api/datasources")[0][3]
        self.assertEqual((made["type"], made["url"], made["name"]), ("prometheus", "http://prom:9090", "Information Broker Prometheus"))
        dash = self.requests("POST", "/api/dashboards/db")[0][3]["dashboard"]
        self.assertEqual([v for v in dash["templating"]["list"] if v["name"] == "ds"][0]["current"]["value"], "new1")


if __name__ == "__main__":
    unittest.main()
