# Grafana — Operations Dashboard

`grafana/dashboards/information-broker-ops.json` is a single operations dashboard
for the Information Broker pipeline: service status, RSS ingest, the summarizer,
Discord delivery, the API, failing feeds, and runtime capacity.

This repo does **not** run Grafana. The dashboard is meant for your own Grafana
server (tested on Grafana 12.2; it uses only core panels: stat, gauge, bar gauge,
time series, table).

- **UID:** `information-broker-ops`
- **Data source:** any Prometheus that scrapes the service's `/metrics` endpoint
  (job `information-broker-metrics`; see `prometheus.yml`)
- **Defaults:** last 24 hours, refresh every 1 minute

## Setting it up

### 1. Metrics reach your Prometheus by remote write

The local Prometheus keeps scraping the app and forwards the samples to the
Prometheus behind your Grafana (`remote_write` in `prometheus.yml`, currently
`http://192.168.0.250:9090/api/v1/write`). Nothing connects *into* this host, and
the local Prometheus stays loopback-only.

The receiving Prometheus must be started with `--web.enable-remote-write-receiver`
and have a clock within a few seconds of this host. Only series with
`job="information-broker-metrics"` plus the alert state (`ALERTS`,
`ALERTS_FOR_STATE`) are forwarded, so the receiving server's own metrics are never
touched. Forwarding starts when it is enabled; earlier history stays in the local
Prometheus.

Check it: on the receiving Prometheus, `up{job="information-broker-metrics"}`
should be `1`; on this host, `prometheus_remote_storage_samples_failed_total`
should stay at `0`. To send somewhere else, change the `url` in `prometheus.yml`
and recreate Prometheus (`docker compose up -d prometheus`).

### 2. Import the dashboard

Either in the Grafana UI: *Dashboards → New → Import*, upload
`grafana/dashboards/information-broker-ops.json`, and pick the data source for the
receiving Prometheus.

Or run the importer (the data source already exists, so `PROMETHEUS_URL` is not
needed and the dashboard keeps its own data source picker):

```bash
GRAFANA_URL=http://192.168.0.250:3000 \
GRAFANA_TOKEN=<service account token with Editor role> \
python3 grafana/import-dashboard.py
```

Create the token under *Administration → Users and access → Service accounts*.
The script sends it only as a Bearer header and never prints it. Set
`PROMETHEUS_URL` as well only if you want the script to create a data source.

## Using it

- **Data source** (top left) picks which Prometheus to read; **Feed** scopes the
  ingest and error panels to some feeds (default: all).
- **Service restarts** (top bar toggle, off by default) draws a marker on every
  chart whenever the service restarted, which explains gaps and queue resets.
- Hover any panel title's *i* icon for what it shows and, on tiles, its thresholds.

## Panels

Colors follow one rule: **green/yellow/orange/red mean status** (good to critical)
and appear only where a value is good or bad; **blue, orange, aqua** identify
series; gray is context. Lines are 2 px with a light area wash.

### Status (two rows of tiles)

| Tile | Shows | Source |
|---|---|---|
| Service | ▲ UP / ▼ DOWN: whether Prometheus can scrape the service | `up{job="information-broker-metrics"}` |
| Alerts firing | Prometheus alerts currently firing (✓ None when clear) | `ALERTS` (rules in `prometheus/rules`, evaluated locally and forwarded) |
| Articles stored | Total articles in the database | `articles_in_database` |
| New articles | Articles first seen in the selected range | `new_articles_found_total` |
| Fetch success | Share of RSS fetches that succeeded | `rss_fetch_total` |
| Summaries OK | Share of summarization requests that succeeded | `summarization_requests_processed_total` |
| Discord delivery | Share of webhook posts that succeeded (— if none) | `discord_webhook_requests_total` |
| Feeds erroring | Feeds with at least one fetch error in the range | `rss_fetch_errors_total` |

### Ingest

| Panel | Shows |
|---|---|
| Articles ingested (rolling hour) | New articles stored in the trailing hour |
| Top sources by new articles | The ten most productive feeds in the range |
| RSS fetches by outcome (per 30 min) | Success vs error per fetch cycle |
| Fetch latency | p50 / p95 / p99 time to fetch and extract a feed's items |

### Summarizer (LLM)

| Panel | Shows |
|---|---|
| Queue fill | Queue depth as a share of its capacity |
| Queue depth | Pending requests against the queue's capacity |
| Time to summarize | Model processing p50 / p95 and queue wait p95 |
| Summaries by outcome (rolling hour) | Requests in the trailing hour by outcome |
| LLM API latency (p95) | p95 latency of calls to the model server |

### Delivery & API

| Panel | Shows |
|---|---|
| Discord posts (per hour) | Webhook posts in the trailing hour by outcome |
| Discord latency (p95) | p95 time to post one article |
| API requests (per min) | The four main endpoints plus everything else |
| API latency (p95) | p95 response time across endpoints |

### Problems

| Panel | Shows |
|---|---|
| Feeds with errors | Table: errors per feed and cause in the range, worst first |
| Fetch errors by type (per 30 min) | Error causes over time |

### Capacity & runtime

| Panel | Shows |
|---|---|
| Articles stored over time | Database growth |
| Database connections | Idle vs in-use connections |
| Memory | Resident memory and heap in use of the service |
| Goroutines | A steady climb suggests a leak |

## Thresholds and baselines

Tile thresholds describe this deployment's normal state, not an ideal:

| Tile | Green | Yellow | Orange | Red |
|---|---|---|---|---|
| Fetch success | ≥ 93% | ≥ 85% | ≥ 70% | below |
| Summaries OK | ≥ 99% | ≥ 95% | ≥ 80% | below |
| Discord delivery | ≥ 99% | ≥ 97% | ≥ 90% | below |
| Feeds erroring | < 15 | ≥ 15 | ≥ 25 | ≥ 40 |
| Queue fill | < 50% | ≥ 50% | ≥ 80% | ≥ 95% |

Roughly a dozen feeds fail permanently (WAF challenges, malformed XML), so fetch
success normally sits near 94% and about 11 feeds show as erroring. Adjust the
thresholds in the panel editor if your baseline differs.

## Why some panels use trailing windows

Ingest is bursty: the RSS monitor fetches every 30 minutes, and summaries and
Discord posts arrive in clumps. A short rate window would draw spikes and gaps, so
those panels use trailing windows that match the cadence (30 minutes for fetches,
one hour for sparse events). If you change `RSS_FETCH_INTERVAL`, edit the `[30m]`
windows to match.

## Editing

Change panels in Grafana, then *Save → Save JSON to file* (or *Export*) and replace
`grafana/dashboards/information-broker-ops.json`. Keep the `ds` data source
variable: it is what lets the file import into any Grafana without editing UIDs.
