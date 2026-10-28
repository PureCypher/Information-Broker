#!/usr/bin/env python3
"""Import the Information Broker dashboard into a Grafana server.

Creates a Prometheus data source when asked, then imports
grafana/dashboards/information-broker-ops.json (overwriting a previous import).

    GRAFANA_URL=http://192.168.0.250:3000 \\
    GRAFANA_TOKEN=<service account token with Editor role> \\
    PROMETHEUS_URL=http://192.168.1.135:9090 \\
    python3 grafana/import-dashboard.py

Environment:
  GRAFANA_URL      required. Base URL of your Grafana server.
  GRAFANA_TOKEN    service account (or API) token with Editor rights. Omit only
                   for an instance that needs no authentication.
  PROMETHEUS_URL   the address your Grafana SERVER uses to reach Prometheus. When
                   set, a data source called DATASOURCE_NAME is created if it does
                   not exist, and the dashboard is pointed at it. When unset, the
                   dashboard keeps its own "Data source" picker.
  DATASOURCE_NAME  default "Information Broker Prometheus".
  FOLDER_UID       optional UID of the Grafana folder to import into.

The token is only ever sent as a Bearer header; it is never printed.
"""
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

DASHBOARD = os.path.join(os.path.dirname(os.path.abspath(__file__)), "dashboards", "information-broker-ops.json")


def call(method, base, path, token, body=None):
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
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
    token = os.environ.get("GRAFANA_TOKEN", "")
    prom = os.environ.get("PROMETHEUS_URL", "").rstrip("/")
    ds_name = os.environ.get("DATASOURCE_NAME", "Information Broker Prometheus")

    with open(DASHBOARD) as f:
        dash = json.load(f)
    dash["id"] = None  # let Grafana assign it; uid keeps re-imports idempotent

    if prom:
        status, found = call("GET", base, "/api/datasources/name/" + urllib.parse.quote(ds_name), token)
        if status == 200:
            uid = found["uid"]
            print("data source %r already exists (url: %s)" % (ds_name, found.get("url")))
        elif status == 404:
            status, made = call("POST", base, "/api/datasources", token, {
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
    status, res = call("POST", base, "/api/dashboards/db", token, payload)
    if status != 200:
        sys.exit("importing the dashboard failed (HTTP %d): %s" % (status, res.get("message", res)))
    print("dashboard imported: %s%s" % (base, res["url"]))


if __name__ == "__main__":
    main()
