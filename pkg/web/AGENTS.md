# pkg/web: web UI and HTTP API

## Live dashboards (`dashboard_live.go`)
- `NotifyStateChanged` runs on the Lua loop for every state change: it only wakes feeds, never
  calls Lua or waits. A feed reads `getValues` from its own goroutine.
- `http.Server.Shutdown` does not wait for or close hijacked connections: `Stop` closes the
  sockets through `live.shutdown` (`TestDashboardLiveShutdown`).
- Entries are compared after a JSON round trip (sorted keys): Lua tables give their keys in any
  order, and a raw compare would send patches for nothing.
- `dashboardValues` is the one place that says how a request reads values and which requests share
  a feed; a viewer whose `getValues` answers differently needs its own key.
- The page's side is `web/static/js/dashboard-live.js`, kept apart from the renderer so
  `testdata/dashboard_live_client.js` can run it in node on a virtual clock with a fake socket
  (`TestDashboardLiveClient`; skipped without node). Put transport logic there, not in
  `dashboard.js`.

## Dashboard texts
- What `dashboard.js` writes comes from `T`, the `dashboard` section of `web/locales/*.json`
  that the page carries as `#dashTexts`; dates and numbers use `locale` from it. A literal or
  `navigator.language` fails `TestDashboardJSHasNoTexts`; a key in one language only fails
  `TestDashboardLocalesSameKeys`.

## Object Manager import (`handle_web_om.go`, `pkg/omproject`)
- A project becomes the om.lua declarations `pkg/omparser` reads (`Project.OMLua`), so a `.omp`
  and a diagnostic pack go through the same `processOMFile`. `TestProjectUploadMatchesDiagnosticPack`
  compares the two on `testdata/omproject/om-*/`: anonymised projects of OM 1.3.5 and 1.8.0 with
  the om.lua their CLUs got (`pack/`). Fixtures from a real house are anonymised first: names
  (rooms too), serials, IPs, keys (`FIXTURE-SECRET-*`), command arguments and script bodies.
- Every value of the project that reaches om.lua is checked against a pattern (`reCLUID`, `reObjID`,
  `reHex`, `reName` in `project.go`); what fails is left out with a warning, never escaped into it.
- `c.Request.FormFile` parses with net/http's 32 MB and ignores `MaxMultipartMemory`; `c.FormFile`
  honours it, which is how `TestProjectUploadKeepsNothingElse` makes the upload spool to a
  temporary file and checks the handler removes it.
