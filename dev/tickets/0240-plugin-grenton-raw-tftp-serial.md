# #240: Plugin permissions `grenton_raw`, `tftp` and `serial`: Grenton frames, file transfer and a serial port

- **Type:** feature
- **Area:** plugins, sandbox
- **Size:** M
- **Epic:** —
- **Blocked by:** #205
- **Skill:** —
- **Needs hardware:** — (a USB RS485 adapter for a final check)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A plugin that declares `grenton_raw` talks to CLUs on the network at the frame level, one that
declares `tftp` reads and writes files on a TFTP server, and one that declares `serial` reads and
writes a serial port the user gave it. The core carries bytes and does the cryptography; what
the frames mean stays in the plugin.

## Context
- #205 adds `permissions` to `plugin.json` (`lan`, `mqtt`, `modbus`); this ticket adds three more
  fields to the same mechanism (`pkg/plugins/permissions.go`).
- vCLU already speaks the CLU protocol for its own needs: `pkg/cipher` (AES-CBC with a project key
  and IV). A plugin cannot reach it, and doing AES in Lua on gopher-lua would be slow and easy to
  get wrong.
- `pkg/tftp` is only a server (vCLU's own files, port 69, started by a command over UDP). A CLU
  serves its files the same way, so a plugin needs the client side; `github.com/pin/tftp/v3`,
  already in `go.mod`, has one (`tftp.NewClient`).
- `tftp` is its own permission because TFTP has no authentication: a plugin that only reads
  frames or sends commands should not also be able to replace files on a device.
- There is no serial port access in the runtime today; Modbus is TCP only.
- `grenton_raw` lets a plugin send any command a CLU accepts with the key it holds; with `tftp`
  it can also replace the CLU's files: together they are full control over that CLU. `serial`
  drives whatever is on the port. All three need the user's explicit consent, not only a line in
  the manifest.

## API
- `plugin:grentonSocket({port = 0})`: a UDP socket of the plugin's own; `send(ip, port, bytes)`,
  `broadcast(port, bytes)`, `onReceive(function(bytes, ip, port) end)`, `close()`.
- `plugin:grentonCipher(key, iv)`: `encrypt(bytes)` / `decrypt(bytes)` with `pkg/cipher`. The
  plugin supplies the key and IV (its config or a file the user points at); the core does not
  hand out the keys from its own configuration.
- `plugin:tftp(ip, {port = 69})`: `get(name)` returns bytes, `put(name, bytes)`; both run off
  the Lua loop and answer in a callback (#190 style), with the library's timeout and retries.
- `plugin:serialOpen(path, {baud = 9600, data = 8, parity = "none", stop = 1})`: `write(bytes)`,
  `onData(function(bytes) end)`, `close()`.

## Files
- `pkg/plugins/permissions.go`, `loader.go`
- `pkg/lua/runtime/core/PluginSandbox.lua`, `PluginAPI.lua`
- New: `pkg/lua/plugin_udp.go`, `pkg/lua/plugin_serial.go` (a serial library in `go.mod`)
- `pkg/cipher`, `pkg/tftp` (a client next to the server)
- `pkg/web/handle_web_plugins.go` and the panel: consent and the port assignment
- `pkg/config/config.go` (serial ports assigned to plugins)

## Acceptance criteria
- [ ] Without `grenton_raw` the plugin has no `grentonSocket` or `grentonCipher`; without `tftp`
      no `tftp`; without `serial` no `serialOpen`.
- [ ] Installing or updating a plugin that adds `grenton_raw`, `tftp` or `serial` waits for the
      user to confirm in the panel, with a warning that says what the permission reaches (CLUs
      the plugin has a key for, files on devices, the device on the port); an update never adds
      them silently.
- [ ] A plugin's UDP socket cannot bind the port vCLU uses for its own CLU traffic and does not
      receive that traffic.
- [ ] The plugin's UDP socket and TFTP client refuse vCLU's own addresses (loopback and the
      host's interfaces): a plugin cannot send commands to or replace files of the vCLU it runs in.
- [ ] `serialOpen` opens only a path the user assigned to that plugin in the panel; a port held by
      another plugin or by vCLU gives an error, not a shared handle.
- [ ] Data arrives in Lua callbacks on the runtime's loop; a slow callback does not lose bytes
      from the port and does not block other plugins.
- [ ] Disabling or removing the plugin closes its sockets and ports and cancels its transfers.
- [ ] Tests: UDP echo and broadcast, encrypt/decrypt against `pkg/cipher` test vectors, TFTP get
      and put against `pkg/tftp`'s server, serial over a pty pair, refusal without the permission,
      port conflicts.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0240-plugin-grenton-raw-tftp-serial.md`.
- [ ] `DEVELOPMENT.md` in `vclu-plugins` documents the three permissions and the consent step.

## Contract impact
Plugin API: three new manifest permissions and the methods above; nothing existing changes. No
decision record needed.
