# Contributing to vCLU

Thanks for your interest in contributing! vCLU is an AI-native smart home runtime — a lightweight, programmable controller with a Lua scripting engine, built-in MCP server for AI agents, and a web-based dashboard. We welcome contributions of all kinds.

## Getting Started

### Prerequisites

- Go 1.24+
- Git

### Build

```bash
git clone https://github.com/Domktorymysli/vclu.git
cd vclu
go build ./cmd/vclu/...
```

### Run locally

```bash
# Auto-detect network interface
./vclu --auto --data-dir ./data

# Or specify interface
./vclu --interface en0 --data-dir ./data
```

Web UI at `http://localhost:8080`. On first run, the setup wizard will guide you through configuration.

### Run tests

```bash
go test ./...
```

## Making Changes

### Branch naming

```
feat/short-description    # new feature
fix/short-description     # bug fix
docs/short-description    # documentation only
chore/short-description   # maintenance, deps, CI
```

### Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add auto-backup scheduler
fix: prevent nil panic in client reporter
docs: add Docker installation guide
chore: update bootstrap to 5.3.3
```

- Use imperative mood ("add" not "added")
- Keep the first line under 72 characters
- One logical change per commit

### Pull requests

1. Fork the repo and create your branch from `main`
2. Make your changes
3. Ensure `gofmt -l .` prints nothing, `go vet ./...` reports no issues and `go test ./...` passes
4. Run `go test -race` on the packages you touched (CI runs it on everything)
5. Open a PR against `main`

Keep PRs focused — one feature or fix per PR. If your change is large, consider splitting it into smaller PRs.

#### PR checklist

- [ ] Code builds without errors
- [ ] Tests pass, also with `-race`
- [ ] New user-visible behaviour has a contract test (see [Contracts](#contracts))
- [ ] No contract test weakened or removed, or the PR says `Contract change:` and why
- [ ] No unrelated changes mixed in

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions short and focused
- Error messages: lowercase, no punctuation (`"failed to read config"` not `"Failed to read config."`)
- Avoid adding dependencies unless truly necessary — the project aims to stay lightweight

### Frontend

- Vanilla JS (no frameworks) — keep it simple
- Use the existing prototype pattern for JS modules (see `backup.js`, `dashboard.js`)
- All user-facing strings must use the i18n system (translations in `web/locales/`)
- CSS follows the existing Bootstrap 5 + dark theme conventions

### Lua runtime

- Files in `pkg/lua/runtime/core/` implement the object system
- User-facing modules go in `modules/` (auto-loaded at startup)
- Always test Lua changes with `go test ./pkg/lua/...`

## Contracts

vCLU runs real houses, and much of its code is written with AI agents. The
behaviour that user scripts, plugins and integrations rely on is pinned down by
contract tests. A change that breaks one of them breaks someone's house.

| Contract | Guarantees | Tests |
|---|---|---|
| State changes | One real change = exactly one `state_changed` event with `old`, `value`, `origin`, `actor`; the same value emits nothing | `pkg/lua/state_change_test.go` |
| Who changed it | Entry points (web, MCP, MQTT/HA, HomeKit, CLU, Satel, modules, plugins) set `origin`/`actor`; timers keep the context they were created in | `pkg/lua/state_context_test.go`, `pkg/mcp/state_context_test.go`, `pkg/web/state_context_test.go` |
| Access control | `readonly`/`hidden` are enforced where commands execute; hidden objects never show up for bots, history included | `pkg/lua/access_*_test.go`, `pkg/lua/ha_*readonly_test.go`, `pkg/mcp/device_history_c0_test.go` |
| Expose | `expose()` options, `filter` thresholds, events of exposed objects | `pkg/lua/exposed_objects_test.go`, `pkg/lua/state_filter_test.go`, `pkg/lua/lua_contract_test.go` |
| Lua runtime | Timers, GPIO polling, MQTT subscriptions and Go functions survive a Lua reload; pending state is flushed at shutdown | `pkg/lua/lua_contract_test.go`, `pkg/lua/lua_timers_test.go`, `pkg/lua/shutdown_test.go` |
| Persistent state | `persist()` and `persist = true` on virtual objects survive reload and restart | `pkg/lua/persist_test.go`, `pkg/lua/virtual_persist_test.go` |
| History | Changes survive a restart without fake entries; a torn file loses at most its last line; deleting data needs a web session | `pkg/history/`, `pkg/lua/history_test.go`, `pkg/web/handlers_history_test.go` |
| Sandbox | `vclu_draft_test` has no real side effects and runs on a virtual clock | `pkg/mcp/sandbox_*_test.go` |
| Grenton protocol | Discovery encryption stays compatible with real CLUs; CLU state subscriptions are kept and renewed | `pkg/cipher/`, `pkg/server/` |

Rules:

- **Do not weaken or delete a contract test to make your change pass.** If a
  contract really has to change, start the PR description with
  `Contract change:`, explain why and how existing user scripts migrate.
- **New user-visible behaviour gets a contract test**: test what a user script
  or an integration sees (events, values, errors), not internals.
- **Tests run without hardware or network.** Use the existing fakes: Lua
  harnesses in `pkg/lua/*_test.go`, the sandbox in `pkg/mcp/`, temp dirs for
  files. A test that needs a real CLU or panel cannot run in CI.
- **No wall-clock sleeps for logic.** Use the virtual clock (sandbox) or an
  injected `now`; when a test must bound real time, loosen it under `-race`.

## Project Structure

```
cmd/vclu/           # main binary entrypoint
pkg/
  config/           # .vclu.json configuration
  server/           # Grenton UDP protocol
  web/              # web UI handlers and routes
  lua/              # Lua engine and Go-Lua bindings
  lua/runtime/      # Lua runtime (bootstrap, core objects)
  mcp/              # MCP (Model Context Protocol) server
  homekit/          # Apple HomeKit bridge
  metrics/          # time-series metrics collector
  accesscontrol/    # profile-based access control
web/
  templates/        # Go HTML templates
  static/           # JS, CSS, vendor libs
  locales/          # i18n translations (en.json, pl.json)
deploy/             # systemd service, install scripts
```

## Reporting Issues

- Use GitHub Issues
- For bugs: include vCLU version (`vclu --version`), OS, and steps to reproduce
- For features: describe the use case, not just the solution

## License

This project uses the [Business Source License 1.1](LICENSE).

**Why BSL?** vCLU is a solo project built over months of evenings and weekends. BSL protects this work from being repackaged and sold as a competing product, while keeping the source fully open for everyone to use, learn from, and contribute to. After 4 years each release converts to Apache 2.0 — fully permissive, no strings attached.

**What this means for you:** You can use vCLU for anything — personal, commercial installations, integrations — except building a competing controller product. Your contributions follow the same terms.

By contributing, you agree that your contributions will be licensed under the [Business Source License 1.1](LICENSE).
