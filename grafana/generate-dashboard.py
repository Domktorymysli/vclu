#!/usr/bin/env python3
"""Generate vCLU Grafana dashboard JSON and push it via API."""

import json
import sys
import urllib.request
import ssl
import base64

DATASOURCE_LOCAL = {"type": "prometheus", "uid": "PBFA97CFB590B2093"}
DATASOURCE_COMMUNITY = {"type": "prometheus", "uid": "${DS_PROMETHEUS}"}
GRAFANA_URL = "https://grafana.lan"
GRAFANA_USER = "admin"
GRAFANA_PASS = "M@slo1111"

# Start with local datasource; we'll patch for community export later
DATASOURCE = DATASOURCE_LOCAL

panel_id = 0
def next_id():
    global panel_id
    panel_id += 1
    return panel_id


def row(title, y, collapsed=False, panels=None):
    return {
        "type": "row",
        "id": next_id(),
        "title": title,
        "collapsed": collapsed,
        "gridPos": {"h": 1, "w": 24, "x": 0, "y": y},
        "panels": panels or [],
    }


def stat_panel(title, targets, gridPos, unit="short", decimals=None,
               thresholds=None, color_mode="value", mappings=None, text_mode="auto"):
    p = {
        "type": "stat",
        "id": next_id(),
        "title": title,
        "datasource": DATASOURCE,
        "gridPos": gridPos,
        "targets": targets,
        "fieldConfig": {
            "defaults": {
                "unit": unit,
                "color": {"mode": "thresholds"},
                "thresholds": thresholds or {
                    "mode": "absolute",
                    "steps": [
                        {"color": "green", "value": None},
                    ],
                },
            },
            "overrides": [],
        },
        "options": {
            "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
            "colorMode": color_mode,
            "graphMode": "area",
            "justifyMode": "auto",
            "textMode": text_mode,
        },
    }
    if decimals is not None:
        p["fieldConfig"]["defaults"]["decimals"] = decimals
    if mappings:
        p["fieldConfig"]["defaults"]["mappings"] = mappings
    return p


def timeseries_panel(title, targets, gridPos, unit="short", decimals=None,
                     fill_opacity=10, legend_mode="list", tooltip_mode="single",
                     stacking=None, overrides=None):
    p = {
        "type": "timeseries",
        "id": next_id(),
        "title": title,
        "datasource": DATASOURCE,
        "gridPos": gridPos,
        "targets": targets,
        "fieldConfig": {
            "defaults": {
                "unit": unit,
                "color": {"mode": "palette-classic"},
                "custom": {
                    "lineWidth": 1,
                    "fillOpacity": fill_opacity,
                    "pointSize": 5,
                    "showPoints": "never",
                    "spanNulls": True,
                    "axisCenteredZero": False,
                    "stacking": stacking or {"mode": "off"},
                },
            },
            "overrides": overrides or [],
        },
        "options": {
            "legend": {"displayMode": legend_mode, "placement": "bottom", "calcs": []},
            "tooltip": {"mode": tooltip_mode, "sort": "none"},
        },
    }
    if decimals is not None:
        p["fieldConfig"]["defaults"]["decimals"] = decimals
    return p


def target(expr, legend="", instant=False):
    t = {
        "datasource": DATASOURCE,
        "expr": expr,
        "legendFormat": legend,
        "refId": "",
    }
    if instant:
        t["instant"] = True
        t["range"] = False
    return t


def assign_ref_ids(targets):
    for i, t in enumerate(targets):
        t["refId"] = chr(65 + i)
    return targets


# ── Build panels ─────────────────────────────────────────────────────
panels = []
y = 0

# ═══════════════════════ SYSTEM HEALTH ═══════════════════════════════
panels.append(row("System Health", y))
y += 1

panels.append(stat_panel(
    "Uptime", assign_ref_ids([target("vclu_uptime_seconds", "uptime", instant=True)]),
    {"x": 0, "y": y, "w": 4, "h": 4}, unit="s", decimals=0,
))
panels.append(stat_panel(
    "Memory", assign_ref_ids([target("vclu_memory_mb", "memory", instant=True)]),
    {"x": 4, "y": y, "w": 4, "h": 4}, unit="decmbytes", decimals=1,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "yellow", "value": 100},
        {"color": "red", "value": 256},
    ]},
))
panels.append(stat_panel(
    "Goroutines", assign_ref_ids([target("vclu_goroutines", "goroutines", instant=True)]),
    {"x": 8, "y": y, "w": 4, "h": 4}, decimals=0,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "yellow", "value": 50},
        {"color": "red", "value": 200},
    ]},
))
panels.append(stat_panel(
    "GC Cycles", assign_ref_ids([target("vclu_gc_runs_total", "gc", instant=True)]),
    {"x": 12, "y": y, "w": 4, "h": 4}, decimals=0,
))
panels.append(stat_panel(
    "Cmd/min", assign_ref_ids([target("vclu_commands_rate", "cmd/min", instant=True)]),
    {"x": 16, "y": y, "w": 4, "h": 4}, decimals=1,
    thresholds={"mode": "absolute", "steps": [
        {"color": "blue", "value": None},
        {"color": "green", "value": 1},
    ]},
))
panels.append(stat_panel(
    "HTTP req/min", assign_ref_ids([target("vclu_http_requests_rate", "http/min", instant=True)]),
    {"x": 20, "y": y, "w": 4, "h": 4}, decimals=1,
    thresholds={"mode": "absolute", "steps": [
        {"color": "blue", "value": None},
        {"color": "green", "value": 1},
    ]},
))
y += 4

panels.append(timeseries_panel(
    "Memory Usage", assign_ref_ids([
        target("vclu_memory_mb", "Memory (MB)"),
    ]),
    {"x": 0, "y": y, "w": 12, "h": 8}, unit="decmbytes", decimals=1,
))
panels.append(timeseries_panel(
    "Goroutines & GC", assign_ref_ids([
        target("vclu_goroutines", "Goroutines"),
        target("rate(vclu_gc_runs_total[5m]) * 60", "GC runs/min"),
    ]),
    {"x": 12, "y": y, "w": 12, "h": 8},
    overrides=[
        {"matcher": {"id": "byName", "options": "GC runs/min"},
         "properties": [{"id": "custom.axisPlacement", "value": "right"},
                        {"id": "unit", "value": "cpm"}]},
    ],
))
y += 8

# ═══════════════════════ LUA ENGINE ══════════════════════════════════
panels.append(row("Lua Engine", y))
y += 1

panels.append(stat_panel(
    "Executions", assign_ref_ids([target("vclu_lua_executions_total", "", instant=True)]),
    {"x": 0, "y": y, "w": 6, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "Errors", assign_ref_ids([target("vclu_lua_errors_total", "", instant=True)]),
    {"x": 6, "y": y, "w": 6, "h": 3}, decimals=0,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "red", "value": 1},
    ]},
))
panels.append(stat_panel(
    "Active Timers", assign_ref_ids([target("vclu_lua_timers_active", "", instant=True)]),
    {"x": 12, "y": y, "w": 6, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "Timers Fired", assign_ref_ids([target("vclu_lua_timers_fired_total", "", instant=True)]),
    {"x": 18, "y": y, "w": 6, "h": 3}, decimals=0,
))
y += 3

panels.append(timeseries_panel(
    "Lua Execution Rate", assign_ref_ids([
        target("rate(vclu_lua_executions_total[5m])", "Executions/s"),
        target("rate(vclu_lua_errors_total[5m])", "Errors/s"),
    ]),
    {"x": 0, "y": y, "w": 12, "h": 8}, unit="ops", decimals=2,
    overrides=[
        {"matcher": {"id": "byName", "options": "Errors/s"},
         "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "red"}}]},
    ],
))
panels.append(timeseries_panel(
    "Timer Activity", assign_ref_ids([
        target("vclu_lua_timers_active", "Active Timers"),
        target("rate(vclu_lua_timers_fired_total[5m])", "Fired/s"),
    ]),
    {"x": 12, "y": y, "w": 12, "h": 8},
    overrides=[
        {"matcher": {"id": "byName", "options": "Fired/s"},
         "properties": [{"id": "custom.axisPlacement", "value": "right"},
                        {"id": "unit", "value": "ops"}]},
    ],
))
y += 8

# ═══════════════════════ CLU COMMANDS ════════════════════════════════
panels.append(row("CLU Commands", y))
y += 1

panels.append(stat_panel(
    "Received", assign_ref_ids([target("vclu_commands_received_total", "", instant=True)]),
    {"x": 0, "y": y, "w": 6, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "Executed", assign_ref_ids([target("vclu_commands_executed_total", "", instant=True)]),
    {"x": 6, "y": y, "w": 6, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "Errors", assign_ref_ids([target("vclu_commands_errors_total", "", instant=True)]),
    {"x": 12, "y": y, "w": 6, "h": 3}, decimals=0,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "red", "value": 1},
    ]},
))
panels.append(stat_panel(
    "Rate", assign_ref_ids([target("vclu_commands_rate", "cmd/min", instant=True)]),
    {"x": 18, "y": y, "w": 6, "h": 3}, unit="cpm", decimals=1,
))
y += 3

panels.append(timeseries_panel(
    "Command Throughput", assign_ref_ids([
        target("rate(vclu_commands_received_total[5m])", "Received/s"),
        target("rate(vclu_commands_executed_total[5m])", "Executed/s"),
        target("rate(vclu_commands_errors_total[5m])", "Errors/s"),
    ]),
    {"x": 0, "y": y, "w": 24, "h": 8}, unit="ops", decimals=2,
    overrides=[
        {"matcher": {"id": "byName", "options": "Errors/s"},
         "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "red"}}]},
    ],
))
y += 8

# ═══════════════════════ MQTT ════════════════════════════════════════
panels.append(row("MQTT", y))
y += 1

panels.append(stat_panel(
    "Connected", assign_ref_ids([target("vclu_mqtt_connected", "", instant=True)]),
    {"x": 0, "y": y, "w": 4, "h": 4}, decimals=0, color_mode="background",
    thresholds={"mode": "absolute", "steps": [
        {"color": "red", "value": None},
        {"color": "green", "value": 1},
    ]},
    mappings=[{"type": "value", "options": {"0": {"text": "Disconnected"}, "1": {"text": "Connected"}}}],
    text_mode="value",
))
panels.append(stat_panel(
    "Published", assign_ref_ids([target("vclu_mqtt_published_total", "", instant=True)]),
    {"x": 4, "y": y, "w": 5, "h": 4}, decimals=0,
))
panels.append(stat_panel(
    "Received", assign_ref_ids([target("vclu_mqtt_received_total", "", instant=True)]),
    {"x": 9, "y": y, "w": 5, "h": 4}, decimals=0,
))
panels.append(stat_panel(
    "Reconnects", assign_ref_ids([target("vclu_mqtt_reconnects_total", "", instant=True)]),
    {"x": 14, "y": y, "w": 5, "h": 4}, decimals=0,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "orange", "value": 1},
        {"color": "red", "value": 10},
    ]},
))
y += 4

panels.append(timeseries_panel(
    "MQTT Traffic", assign_ref_ids([
        target("rate(vclu_mqtt_published_total[5m])", "Published/s"),
        target("rate(vclu_mqtt_received_total[5m])", "Received/s"),
    ]),
    {"x": 0, "y": y, "w": 12, "h": 8}, unit="ops", decimals=2,
))
panels.append(timeseries_panel(
    "MQTT Reconnects", assign_ref_ids([
        target("increase(vclu_mqtt_reconnects_total[5m])", "Reconnects (5m)"),
    ]),
    {"x": 12, "y": y, "w": 12, "h": 8}, decimals=0,
    overrides=[
        {"matcher": {"id": "byName", "options": "Reconnects (5m)"},
         "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "orange"}},
                        {"id": "custom.fillOpacity", "value": 30}]},
    ],
))
y += 8

# ═══════════════════════ HTTP ════════════════════════════════════════
panels.append(row("HTTP Server", y))
y += 1

panels.append(stat_panel(
    "Total Requests", assign_ref_ids([target("vclu_http_requests_total", "", instant=True)]),
    {"x": 0, "y": y, "w": 6, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "4xx Errors", assign_ref_ids([target("vclu_http_errors_4xx_total", "", instant=True)]),
    {"x": 6, "y": y, "w": 6, "h": 3}, decimals=0,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "yellow", "value": 1},
    ]},
))
panels.append(stat_panel(
    "5xx Errors", assign_ref_ids([target("vclu_http_errors_5xx_total", "", instant=True)]),
    {"x": 12, "y": y, "w": 6, "h": 3}, decimals=0,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "red", "value": 1},
    ]},
))
panels.append(stat_panel(
    "Req/min", assign_ref_ids([target("vclu_http_requests_rate", "", instant=True)]),
    {"x": 18, "y": y, "w": 6, "h": 3}, unit="reqps", decimals=1,
))
y += 3

panels.append(timeseries_panel(
    "HTTP Request Rate", assign_ref_ids([
        target("rate(vclu_http_requests_total[5m])", "Total/s"),
        target("rate(vclu_http_requests_api_total[5m])", "API/s"),
        target("rate(vclu_http_requests_web_total[5m])", "Web/s"),
    ]),
    {"x": 0, "y": y, "w": 12, "h": 8}, unit="reqps", decimals=2,
))
panels.append(timeseries_panel(
    "HTTP Errors", assign_ref_ids([
        target("rate(vclu_http_errors_4xx_total[5m])", "4xx/s"),
        target("rate(vclu_http_errors_5xx_total[5m])", "5xx/s"),
    ]),
    {"x": 12, "y": y, "w": 12, "h": 8}, unit="reqps", decimals=2,
    overrides=[
        {"matcher": {"id": "byName", "options": "4xx/s"},
         "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "yellow"}}]},
        {"matcher": {"id": "byName", "options": "5xx/s"},
         "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "red"}}]},
    ],
))
y += 8

# ═══════════════════════ HOMEKIT ═════════════════════════════════════
panels.append(row("HomeKit", y))
y += 1

panels.append(stat_panel(
    "Accessories", assign_ref_ids([target("vclu_homekit_accessories", "", instant=True)]),
    {"x": 0, "y": y, "w": 6, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "Paired", assign_ref_ids([target("vclu_homekit_paired", "", instant=True)]),
    {"x": 6, "y": y, "w": 6, "h": 3}, decimals=0, color_mode="background",
    thresholds={"mode": "absolute", "steps": [
        {"color": "red", "value": None},
        {"color": "green", "value": 1},
    ]},
    mappings=[{"type": "value", "options": {"0": {"text": "Not Paired"}, "1": {"text": "Paired"}}}],
    text_mode="value",
))
panels.append(stat_panel(
    "Total Requests", assign_ref_ids([target("vclu_homekit_requests_total", "", instant=True)]),
    {"x": 12, "y": y, "w": 6, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "Events Sent", assign_ref_ids([target("vclu_homekit_events_total", "", instant=True)]),
    {"x": 18, "y": y, "w": 6, "h": 3}, decimals=0,
))
y += 3

panels.append(timeseries_panel(
    "HomeKit Request Rate", assign_ref_ids([
        target("rate(vclu_homekit_get_total[5m])", "GET/s"),
        target("rate(vclu_homekit_set_total[5m])", "SET/s"),
        target("rate(vclu_homekit_events_total[5m])", "Events/s"),
    ]),
    {"x": 0, "y": y, "w": 24, "h": 8}, unit="ops", decimals=2,
))
y += 8

# ═══════════════════════ MCP & PLUGINS ═══════════════════════════════
panels.append(row("MCP & Plugins", y))
y += 1

panels.append(stat_panel(
    "MCP Sessions", assign_ref_ids([target("vclu_mcp_active_sessions", "", instant=True)]),
    {"x": 0, "y": y, "w": 4, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "MCP Requests", assign_ref_ids([target("vclu_mcp_requests_total", "", instant=True)]),
    {"x": 4, "y": y, "w": 4, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "MCP Errors", assign_ref_ids([target("vclu_mcp_errors_total", "", instant=True)]),
    {"x": 8, "y": y, "w": 4, "h": 3}, decimals=0,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "red", "value": 1},
    ]},
))
panels.append(stat_panel(
    "Active Plugins", assign_ref_ids([target("vclu_plugins_active", "", instant=True)]),
    {"x": 12, "y": y, "w": 4, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "Plugin HTTP Reqs", assign_ref_ids([target("vclu_plugin_http_requests_total", "", instant=True)]),
    {"x": 16, "y": y, "w": 4, "h": 3}, decimals=0,
))
panels.append(stat_panel(
    "Plugin HTTP Errs", assign_ref_ids([target("vclu_plugin_http_errors_total", "", instant=True)]),
    {"x": 20, "y": y, "w": 4, "h": 3}, decimals=0,
    thresholds={"mode": "absolute", "steps": [
        {"color": "green", "value": None},
        {"color": "red", "value": 1},
    ]},
))
y += 3

panels.append(timeseries_panel(
    "MCP Activity", assign_ref_ids([
        target("rate(vclu_mcp_requests_total[5m])", "Requests/s"),
        target("rate(vclu_mcp_errors_total[5m])", "Errors/s"),
        target("vclu_mcp_active_sessions", "Active Sessions"),
    ]),
    {"x": 0, "y": y, "w": 12, "h": 8}, unit="ops", decimals=2,
    overrides=[
        {"matcher": {"id": "byName", "options": "Errors/s"},
         "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "red"}}]},
        {"matcher": {"id": "byName", "options": "Active Sessions"},
         "properties": [{"id": "custom.axisPlacement", "value": "right"},
                        {"id": "unit", "value": "short"}]},
    ],
))
panels.append(timeseries_panel(
    "Plugin HTTP Activity", assign_ref_ids([
        target("rate(vclu_plugin_http_requests_total[5m])", "Requests/s"),
        target("rate(vclu_plugin_http_errors_total[5m])", "Errors/s"),
    ]),
    {"x": 12, "y": y, "w": 12, "h": 8}, unit="ops", decimals=2,
    overrides=[
        {"matcher": {"id": "byName", "options": "Errors/s"},
         "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "red"}}]},
    ],
))

# ── Assemble dashboard ───────────────────────────────────────────────
dashboard = {
    "dashboard": {
        "id": None,
        "uid": "vclu-overview",
        "title": "vCLU Overview",
        "tags": ["vclu", "iot"],
        "timezone": "browser",
        "schemaVersion": 39,
        "version": 0,
        "refresh": "30s",
        "time": {"from": "now-6h", "to": "now"},
        "timepicker": {
            "refresh_intervals": ["5s", "10s", "30s", "1m", "5m"],
        },
        "annotations": {
            "list": [{
                "builtIn": 1,
                "datasource": {"type": "grafana", "uid": "-- Grafana --"},
                "enable": True,
                "hide": True,
                "iconColor": "rgba(0, 211, 255, 1)",
                "name": "Annotations & Alerts",
                "type": "dashboard",
            }],
        },
        "panels": panels,
        "templating": {"list": []},
        "links": [],
        "editable": True,
        "fiscalYearStartMonth": 0,
        "graphTooltip": 1,  # shared crosshair
        "liveNow": False,
    },
    "overwrite": True,
    "folderUid": "",
}

# ── Save local JSON (API format) ─────────────────────────────────────
json_path = "grafana/vclu-dashboard.json"
with open(json_path, "w") as f:
    json.dump(dashboard, f, indent=2)
print(f"Local dashboard saved to {json_path}")
print(f"Total panels: {len(panels)}")

# ── Generate community-shareable JSON ────────────────────────────────
import copy

def replace_datasource_uid(obj, old_uid, new_uid):
    """Recursively replace datasource UIDs in the dashboard JSON."""
    if isinstance(obj, dict):
        for k, v in obj.items():
            if k == "uid" and v == old_uid:
                obj[k] = new_uid
            else:
                replace_datasource_uid(v, old_uid, new_uid)
    elif isinstance(obj, list):
        for item in obj:
            replace_datasource_uid(item, old_uid, new_uid)

community_db = copy.deepcopy(dashboard["dashboard"])
replace_datasource_uid(community_db, "PBFA97CFB590B2093", "${DS_PROMETHEUS}")

# Remove local-specific fields
community_db["id"] = None
community_db["uid"] = None
community_db["version"] = 0
community_db["description"] = (
    "Monitoring dashboard for vCLU (Virtual CLU) — a smart home controller "
    "compatible with Grenton ecosystem. Displays 35 Prometheus metrics covering "
    "system health, Lua engine, MQTT, HTTP server, HomeKit, MCP, and plugins."
)

# Add __inputs (Grafana.com import convention)
community_db["__inputs"] = [
    {
        "name": "DS_PROMETHEUS",
        "label": "Prometheus",
        "description": "Prometheus datasource that scrapes vCLU /metrics endpoint",
        "type": "datasource",
        "pluginId": "prometheus",
        "pluginName": "Prometheus",
    }
]

# Add __requires (declares panel plugin dependencies)
community_db["__requires"] = [
    {"type": "grafana", "id": "grafana", "name": "Grafana", "version": "10.0.0"},
    {"type": "datasource", "id": "prometheus", "name": "Prometheus", "version": "1.0.0"},
    {"type": "panel", "id": "stat", "name": "Stat", "version": ""},
    {"type": "panel", "id": "timeseries", "name": "Time series", "version": ""},
]

community_path = "grafana/vclu-dashboard-community.json"
with open(community_path, "w") as f:
    json.dump(community_db, f, indent=2)
print(f"Community dashboard saved to {community_path}")

# ── Push local version to Grafana ────────────────────────────────────
try:
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE

    creds = base64.b64encode(f"{GRAFANA_USER}:{GRAFANA_PASS}".encode()).decode()
    req = urllib.request.Request(
        f"{GRAFANA_URL}/api/dashboards/db",
        data=json.dumps(dashboard).encode(),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Basic {creds}",
        },
        method="POST",
    )
    resp = urllib.request.urlopen(req, context=ctx)
    result = json.loads(resp.read())
    print(f"Dashboard pushed: {GRAFANA_URL}{result.get('url', '')}")
except Exception as e:
    print(f"API push failed: {e}")
    print("You can import the JSON manually via Grafana UI → Dashboards → Import")