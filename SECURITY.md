# Security policy

vCLU controls real houses: lights, covers, heating and, through Satel, the
alarm. Please report security problems privately.

## Reporting a vulnerability

Use **GitHub private vulnerability reporting**: the repository's
**Security** tab -> **Report a vulnerability**. Do not open a public issue,
discussion or pull request for a security problem.

Please include:

- the vCLU version (`vclu --version`) and how it runs (binary, Docker, OS),
- what an attacker can do and from where (LAN, HA/MQTT, an MCP bot, a plugin,
  a Lua module),
- steps to reproduce, ideally without real hardware.

We aim to answer within 7 days. A fix ships in a new release; tell us if you
want to be credited.

## Supported versions

Only the latest release gets security fixes.

## Scope

In scope, for example:

- bypassing login, sessions, CSRF protection or API key / bot token checks,
- controlling or reading an object that is `readonly` or `hidden` for that
  client (MQTT/Home Assistant, HomeKit, MCP bots),
- running code or writing files without Dev mode or Exec mode,
- a draft test (`vclu_draft_test`) starting processes, writing files or
  reaching real devices,
- a plugin escaping its sandbox,
- disarming or force-arming Satel when it is not allowed in the configuration,
- leaking secrets (passwords, PINs, tokens, keys) through the API, logs or MCP.

Not vulnerabilities:

- what Dev mode and Exec mode are meant to do: a bot with these permissions
  can change automations and run Lua on the live runtime. A promoted module
  runs with the full rights of the vCLU process (files, processes), so Dev
  mode amounts to code execution on the host. Grant it only to bots you
  trust,
- attacks that need an admin login or shell access to the host,
- the Grenton protocol itself (vCLU has to stay compatible with real CLUs).

## Deployment advice

- Run vCLU on a trusted local network. Do not expose the web UI, the MCP
  endpoint or the MQTT broker directly to the internet; use a VPN.
- Keep Dev mode off when you do not need it (it has a kill switch in the web
  UI) and give each bot its own token and access profile.
