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
