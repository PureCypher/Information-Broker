# Grafana — Operations Overview Dashboard

`grafana/dashboards/information-broker-overview.json` is the single, consolidated
"single pane of glass" for the Information Broker pipeline. It replaces the five
earlier dashboards (general overview + the four detail dashboards) so an operator
can spot problems at a glance and read the key stats without switching views.

- **UID:** `information-broker-overview`
- **Datasource:** Prometheus (`uid: prometheus`)
- **Default time range:** last 24h · **Refresh:** 30s
- **Home dashboard:** set as the Grafana landing page (see
  `GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH` in `docker-compose.yml`, and the
  org preference applied at runtime).

## Layout

The dashboard is organised into four labelled sections, problems first.

### ① Service Health & SLOs (stat tiles, colour-coded)
| Tile | Query (summary) | Healthy |
|------|-----------------|---------|
| Service Up | `up{job="information-broker-metrics"}` | UP (green) |
| RSS Fetch Success | success ÷ total `rss_fetch_total` over range | ≥ 97% |
| DB Save Success | success ÷ total `articles_processed_total` over range | ≥ 98% |
| Discord Delivery | success ÷ total `discord_webhook_requests_total` | ≥ 99% |
| Summaries OK | success ÷ total `summarization_requests_processed_total` | ≥ 99% |
| Feeds Erroring | distinct feeds with `rss_fetch_errors_total` > 0 in range | 0 |
| Articles in DB | `articles_in_database` | — |
| New Articles (range) | `increase(new_articles_found_total[range])` | — |

`Service Up` keys off the **metrics** scrape job specifically, so a struggling
auxiliary endpoint never produces a false red.

### ② Pipeline Throughput & Latency
- **Pipeline Throughput (events/min):** the content funnel — RSS fetched → new
  found → saved → summarized → Discord-delivered, each as a per-minute rate.
- **Stage Latency (p95):** per-stage 95th-percentile latency. RSS uses the auto
  rate window; the bursty summarize / Discord / pipeline stages use a 1h window
  so the line stays continuous (a short window would be empty between bursts and
  render as `NaN`).

### ③ Problems — Errors & Failing Feeds
- **Top Feeds by Fetch Errors:** instant table (`format: table`) of feeds with
  errors in the range, broken down by `error_type`, sorted desc, colour-graded.
  This is the "what is broken right now" list.
- **Error Rate by Type:** stacked timeseries of every failure mode — RSS errors
  by type, summarization API errors, Discord errors, and DB save failures.

### ④ Content Volume & Capacity
- **Daily Ingestion (last 30 days):** saved/day vs. save-failed/day bars
  (pinned to a 30-day window independent of the dashboard time range).
- **Summarization Queue:** `summarization_queue_depth` vs. `_capacity`.
- **Database Connections:** pool usage by state (open / in_use / idle).

## Deployment

Dashboards are file-provisioned (`grafana/provisioning/dashboards/dashboard.yml`
→ `/var/lib/grafana/dashboards`, reloaded every 10s). Dropping the JSON into
`grafana/dashboards/` loads it; removing a file removes it from Grafana. No API
import step is required.

## Related fixes shipped alongside this dashboard

1. **Container healthcheck** — the runtime image is `FROM scratch` (no
   shell/wget/curl), so the previous `wget`-based healthcheck could never run and
   the app showed a false `unhealthy`. `healthcheck.go` implements the
   `-health-check` flag (an HTTP self-probe of `/health`) that the Dockerfile and
   `docker-compose.yml` already invoke. Takes effect on the next image rebuild.
2. **Prometheus `/health` job removed** — it scraped `/health` (JSON) as if it
   were Prometheus text, producing a permanently-down target. Liveness comes from
   the `information-broker-metrics` job's `up` series.
