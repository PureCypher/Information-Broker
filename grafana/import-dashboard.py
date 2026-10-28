#!/usr/bin/env python3
"""Import the Information Broker dashboard into a Grafana server.

Imports grafana/dashboards/information-broker-ops.json (overwriting a previous
import) and points it at a Prometheus data source.

    # an existing data source, by name
    GRAFANA_URL=http://192.168.0.250:3000 GRAFANA_TOKEN=<token> \\
    DATASOURCE_NAME=Prometheus python3 grafana/import-dashboard.py

    # or create one
    GRAFANA_URL=http://192.168.0.250:3000 GRAFANA_TOKEN=<token> \\
    PROMETHEUS_URL=http://192.168.0.250:9090 python3 grafana/import-dashboard.py

Environment:
  GRAFANA_URL       required. Base URL of your Grafana server.
  GRAFANA_TOKEN     service account (or API) token with Editor rights. Preferred.
  GRAFANA_USER /
  GRAFANA_PASSWORD  basic-auth login, used only when GRAFANA_TOKEN is not set.
                    With neither, requests are sent unauthenticated.
  DATASOURCE_NAME   name of the Prometheus data source the dashboard should use.
                    It must already exist unless PROMETHEUS_URL is also set.
  PROMETHEUS_URL    the address your Grafana SERVER uses to reach Prometheus. When
                    set, the data source (DATASOURCE_NAME, default "Information
                    Broker Prometheus") is created if it does not exist.
  With neither DATASOURCE_NAME nor PROMETHEUS_URL the dashboard keeps its own
  "Data source" picker.
  FOLDER_UID        optional UID of the Grafana folder to import into.

Credentials are only ever sent in the Authorization header; they are never printed.
"""
import base64
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

DASHBOARD = os.path.join(os.path.dirname(os.path.abspath(__file__)), "dashboards", "information-broker-ops.json")


def auth_header():
    """Bearer token if set, else basic auth from user+password, else nothing."""
    token = os.environ.get("GRAFANA_TOKEN", "")
    if token:
        return "Bearer " + token
    user, password = os.environ.get("GRAFANA_USER", ""), os.environ.get("GRAFANA_PASSWORD", "")
    if user and password:
        return "Basic " + base64.b64encode(("%s:%s" % (user, password)).encode()).decode()
    return ""


def call(method, base, path, auth, body=None):
    headers = {"Accept": "application/json"}
    if auth:
        headers["Authorization"] = auth
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, json.load(resp)
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.load(e)
        except ValueError:
            return e.code, {}
    except urllib.error.URLError as e:
        sys.exit("cannot reach %s: %s" % (base, e.reason))


def main():
    base = os.environ.get("GRAFANA_URL", "").rstrip("/")
    if not base:
        sys.exit("GRAFANA_URL is required (see the docstring at the top of this file)")
    auth = auth_header()
    prom = os.environ.get("PROMETHEUS_URL", "").rstrip("/")
    ds_name = os.environ.get("DATASOURCE_NAME", "") or ("Information Broker Prometheus" if prom else "")

    with open(DASHBOARD) as f:
        dash = json.load(f)
    dash["id"] = None  # let Grafana assign it; uid keeps re-imports idempotent

    if ds_name:
        status, found = call("GET", base, "/api/datasources/name/" + urllib.parse.quote(ds_name), auth)
        if status == 200:
            uid = found["uid"]
            print("using existing data source %r (url: %s)" % (ds_name, found.get("url")))
        elif status == 404 and not prom:
            sys.exit("data source %r not found; create it, or set PROMETHEUS_URL so this script does" % ds_name)
        elif status == 404:
            status, made = call("POST", base, "/api/datasources", auth, {
                "name": ds_name, "type": "prometheus", "access": "proxy", "url": prom})
            if status != 200:
                sys.exit("creating the data source failed (HTTP %d): %s" % (status, made.get("message", made)))
            uid = made["datasource"]["uid"]
            print("created data source %r -> %s" % (ds_name, prom))
        else:
            sys.exit("looking up the data source failed (HTTP %d): %s" % (status, found.get("message", found)))
        for var in dash["templating"]["list"]:
            if var["name"] == "ds":
                var["current"] = {"selected": True, "text": ds_name, "value": uid}

    payload = {"dashboard": dash, "overwrite": True, "message": "Imported by import-dashboard.py"}
    if os.environ.get("FOLDER_UID"):
        payload["folderUid"] = os.environ["FOLDER_UID"]
    status, res = call("POST", base, "/api/dashboards/db", auth, payload)
    if status != 200:
        sys.exit("importing the dashboard failed (HTTP %d): %s" % (status, res.get("message", res)))
    print("dashboard imported: %s%s" % (base, res["url"]))


if __name__ == "__main__":
    main()
