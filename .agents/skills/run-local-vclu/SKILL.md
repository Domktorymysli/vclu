---
name: run-local-vclu
description: "Run vCLU locally without hardware (demo house of virtual objects, fresh data dir), drive it through the HTTP API, read history, and take screenshots of its pages. Use to check a change end to end in a running vCLU, reproduce a UI bug, or make README/docs images."
---

# Run vCLU locally

**When:** unit tests are not enough and you need a running vCLU: a dashboard, history, the editor,
the MQTT/HA layer seen from outside, or screenshots. No Grenton CLU, no MQTT broker, no real house.

**Read first:** `tools/demo/modules/` (the demo house); `docs/ai/examples.md` for the Lua API.

## Steps

1. Start it in the background: `tools/demo/run.sh [port]` (default 18080). It builds `cmd/vclu`,
   creates a fresh data dir (printed; set `DEMO_DIR` to reuse one), copies `tools/demo/modules/`
   into `data/clu/modules/` and links `web/`. Wait for `loaded 1/1 user module(s)` in its output.
   It sets `VCLU_UPDATE_CHECK=off`, so the run does not ask for new versions; set it yourself
   when you start `vclu` another way.
2. Change the demo for your case by editing `$DEMO_DIR/data/clu/modules/` and restarting the
   process. Keep `tools/demo/modules/` generic; add to it only what most checks need.
3. Drive it like a user, through the dashboard API:
   `curl -X POST -H 'Content-Type: application/json' -d '{"widgetId":"kitchen","action":"toggle","value":true}' http://127.0.0.1:18080/api/dashboard/home/action`.
   Slider: `"action":"set","value":35`; select: `"action":"select","value":"Night"`.
   List dashboards: `GET /api/dashboards`; current values: `GET /api/dashboard/home/values`.
4. Read what happened: `GET /api/history` (newest first, with `origin` and `actor`).
   Start from a clean history: `POST /api/history/clear-memory` and `/api/history/clear-disk`,
   each with the body `{"confirm":true}`.
5. Screenshots: `go run ./tools/screenshot [-w 1280] [-h 800] <url> <out.png> [js-step ...]`
   (needs Chrome; `CHROME=` for another binary). Add `?lang=en` to the URL for the English UI.
   Pages: `/d/home`, `/history`, `/editor`. JS steps run in the page in order, e.g. clicking a
   file in the editor tree:
   `"[...document.querySelectorAll('#fileTree *')].find(e => e.textContent.trim() === 'demo').click()"`.
6. Stop the process when done. Do not leave it running.

## Pitfalls

- The web server listens on **all interfaces**; `-ip` binds only the CLU protocol. A fresh data
  dir has no password, so anyone on the LAN can open it while it runs.
- `web/` is found relative to the working directory: without it the process panics on
  `web/templates/*.html`. `run.sh` handles this.
- History records only exposed objects (`expose()`); a `VirtualSwitch` that is not exposed changes
  without a trace. Automation changes show an `automation:module:...` actor.
- Timers in user modules: `setTimeout(fn, ms)` and `EventBus:getShared():setInterval(fn, ms)`.
  There is no `Timer` object.
- `require()` of a module that returns `nil` gives `true`: guard with
  `if type(m) ~= "table" then return end` in dashboards.
- Screenshots for the repository: demo data only, no real names, addresses, IPs, serials or tokens.
  The default `-dpr 1.25` keeps a 1280×800 page under 300 KB; downscaling a `-dpr 2` image makes it larger.

## Check

- The output shows `loaded 1/1 user module(s)` and no `ERROR`.
- What you changed is visible through the API or a screenshot you looked at, not only assumed.
- The process is stopped and nothing from `$DEMO_DIR` ended up in the commit.
