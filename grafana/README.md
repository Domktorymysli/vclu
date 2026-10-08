# vCLU Grafana Dashboard

Monitoring dashboard for vCLU — 35 Prometheus metrics across 7 sections:
System Health, Lua Engine, CLU Commands, MQTT, HTTP Server, HomeKit, MCP & Plugins.

## Prerequisites

- vCLU with `/metrics` endpoint enabled
- Prometheus scraping vCLU (see below)
- Grafana with Prometheus datasource

## Prometheus scrape config

Add to `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'vclu'
    scrape_interval: 15s
    static_configs:
      - targets: ['<VCLU_HOST>:<VCLU_PORT>']
    # If vCLU has auth enabled, add API key:
    # http_headers:
    #   X-API-Key:
    #     values:
    #       - '<your-vclu-api-key>'
```

## Import dashboard

### Option A: Grafana UI

1. Open Grafana → Dashboards → **Import**
2. Click **"Upload dashboard JSON file"**
3. Select `vclu-dashboard-community.json`
4. Pick your Prometheus datasource from the dropdown
5. Click **Import**

### Option B: API

```bash
# Adjust URL and credentials
curl -X POST https://grafana.example.com/api/dashboards/db \
  -H 'Content-Type: application/json' \
  -u admin:password \
  -d @vclu-dashboard.json
```

## Files

| File | Purpose |
|------|---------|
| `vclu-dashboard-community.json` | Shareable export (datasource as variable) |
| `vclu-dashboard.json` | Local deploy format (API wrapper, hardcoded datasource UID) |
| `generate-dashboard.py` | Generates both JSON files and pushes to local Grafana |

## Regenerate

```bash
python3 grafana/generate-dashboard.py
```