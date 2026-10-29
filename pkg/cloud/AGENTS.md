# pkg/cloud: the vclu cloud client

- The protocol is `docs/protocol.md` in `../vclu-cloud`; its types come from
  `github.com/Domktorymysli/vclu-cloud/proto` through the `replace` in `go.mod`, so this branch
  builds only with `../vclu-cloud` checked out next to it (not in Docker or CI).
- Test against the fake cloud in `client_test.go`, or end to end with the real cloud from
  `../vclu-cloud` (`VCLU_CLOUD_MAIL=log`) and `cloud.url` in `.vclu.json` pointing at it.
- Close codes arrive as `*websocket.CloseError` from the first failed read of `wsConn`; yamux
  sees only an error, so `ended` asks `wsConn.closeCode`, not the stream.
- Pairing and unpairing are refused to API keys (`cloudAdmin` in `pkg/web/handle_web_cloud.go`):
  whoever enters the code attaches the house to their account.
- Notifications: `Cloud.notify` in Lua (`pkg/lua/runtime/core/Cloud.lua`) encodes the protocol's
  `notify`; `Client.Notify` sends it or queues it while the connection is down (10 minutes, 20 at
  most). A pressed button comes back as `notify_action` and runs `Cloud._action` through
  `Options.NotifyAction` (`cmd/vclu/cloud.go`) as the person (`CallAs("remote", "cloud:<id>")`),
  at most once per request id.
- Remote dashboards: the cloud opens one yamux stream per HTTP request; `connect` serves them with
  `Options.Tunnel` (`web.Server.TunnelHandler`, `pkg/web/tunnel.go`), the only place that believes
  `X-Vclu-Actor` / `X-Vclu-Role`. It passes the role to `Dashboard.API.*` as the last argument;
  `:remote()` in `Dashboard.lua` decides what each role gets. Cached dashboard answers are keyed by
  role (`roleKey`). The `dashboards` message comes from `Dashboard.API.remoteList()` after welcome
  and after every Lua reload (`DashboardsChanged`). Tests: `pkg/web/tunnel_test.go`.
- Actions from phones on a weak signal: `actionId` in the action body makes a repeat return the
  first answer instead of running again, and `check` asks how it went without running anything
  (`pkg/web/action_replay.go`, keyed by `limitKey`, so per person through the cloud). Tests:
  `pkg/web/action_replay_test.go`.
- The app on the LAN: `pkg/web/local.go`. The cloud asks for a token on `proto.PathLocalToken`
  through the tunnel (only `TunnelHandler` serves it); `LocalHandler` serves the remote routes on
  `cloud.localPort` (9443) over TLS with a self-signed certificate (`cloud-local.crt`/`.key` in
  the CLU directory) to `Authorization: Bearer vclu_local_…`. A token counts only while the last
  `members` list (`Options.Members`, kept in `cloud-local.json`) names its person and app, with
  the role from there. Tests: `pkg/web/local_test.go`.
