# Security policy

vclu controls real houses: lights, covers, heating, gates and, through Satel, the
alarm. Please report security problems privately.

## Reporting a vulnerability

Use **GitHub private vulnerability reporting**: the repository's
**Security** tab -> **Report a vulnerability**. Without a GitHub account, write
to **security@vclu.pl**. Do not open a public issue, discussion or pull request
for a security problem.

Please include:

- the vclu version (`vclu --version`) and how it runs (binary, `.deb`, Docker,
  OS), or the app and its version,
- what an attacker can do and from where (the internet, the cloud, LAN,
  HA/MQTT, an MCP bot, a plugin, a Lua module),
- steps to reproduce, ideally without real hardware.

Test the cloud only with your own account and your own house; do not access
other people's data or degrade the service. Reports made in good faith that
follow this policy are welcome, and we will not take action against them.

## What happens next

- **Acknowledgement** within 3 working days.
- **First assessment** within 10 days: whether it is a vulnerability, how
  severe, and what happens next. We keep you informed until it is fixed.
- **Fix:** critical and high severity problems get a release of their own as
  soon as the fix is ready; others ship in the next regular release.
- **Disclosure** together with the fix, as a GitHub security advisory. Tell us
  if you want to be credited.

A vulnerability that is actively exploited, and an incident that affects the
security of vclu, are reported to the authorities as the EU Cyber Resilience
Act (Regulation 2024/2847) requires, through the single reporting platform of
ENISA (an early warning within 24 hours, a notification within 72 hours, a
final report), and users are told what to do.

## Supported versions

Security fixes ship in a new release; only the latest release gets them.
Releases are frequent and upgrades are free, so upgrading is how a fix
arrives: `sudo apt upgrade` with the APT repository, a new image with Docker,
**Update** in the web UI otherwise. Each release says in its notes whether it
fixes a security problem.

The cloud (`cloud.vclu.pl`) is always on its latest version. The mobile apps
are updated through Google Play and the App Store.

A long-term support line, with security fixes for a longer period, is to be
announced.

## How fixes are announced

- a **GitHub security advisory** in this repository, with a CVE when one
  applies,
- a **Security** line in the release notes and `CHANGELOG.md`,
- for a problem that needs action from users (beyond upgrading), also on
  [vclu.pl](https://vclu.pl).

## Verifying what you install

- Release assets are listed with their SHA-256 in `checksums.txt` of each
  [release](https://github.com/Domktorymysli/vclu/releases):
  `sha256sum -c checksums.txt --ignore-missing`.
- The APT repository (`apt.vclu.pl`) is signed; its key is kept offline, never
  in CI ([deploy/apt/README.md](deploy/apt/README.md)).
- Docker images are published to `ghcr.io/domktorymysli/vclu` by the release
  workflow only.
- Every release has an SPDX SBOM of each archive, `.deb` package and image
  (`*.spdx.json` among its assets).

## Security records

Every release keeps a record of how it was checked, as release assets:

- `vclu-X.Y.Z-security.json`: the commit and tag it was built from, the Go
  version, its SBOMs with their SHA-256, and every scan of it: the date, the
  scanners and their versions (govulncheck for the Go code, Trivy for the image,
  OSV for the bundled web libraries), the result and the public advisory ids
  found. An advisory that affects the release is listed with how it was
  handled: the release and the GitHub security advisory that fixed it, or why
  vclu is not affected.
- `vclu-X.Y.Z-scan-<time>-<kind>.tar.gz`: the scanners' raw output, from the
  scan before the release and from each weekly scan after it.

The latest release is scanned again every week, since new advisories come out
after a release, and each scan is added to its record. Findings reviewed and
accepted as not affecting vclu are listed with the reason in
[.github/govulncheck-accepted.txt](.github/govulncheck-accepted.txt) and
[.github/scan-accepted.txt](.github/scan-accepted.txt).

## Scope

vclu, the vclu cloud and the mobile apps are one product; all of them are in
scope. For example:

- bypassing login, sessions, CSRF protection or API key / bot token checks,
- controlling or reading an object that is `readonly` or `hidden` for that
  client (MQTT/Home Assistant, HomeKit, MCP bots, the cloud),
- running code or writing files without Dev mode or Exec mode,
- a draft test (`vclu_draft_test`) starting processes, writing files or
  reaching real devices,
- a plugin escaping its sandbox,
- disarming or force-arming Satel when it is not allowed in the configuration,
- leaking secrets (passwords, PINs, tokens, keys) through the API, logs or MCP,
- the cloud: accounts, pairing a vclu, the tunnel to a house, remote
  dashboards, voice control and Google Home: reaching or controlling a house
  that is not yours, or one that was not shared with you,
- the mobile apps: their sessions and tokens, notifications and actions.

Official plugins (`@vclu/*`) are in scope. Community plugins belong to their
authors: report to them; if a plugin can harm users beyond its own purpose,
tell us too.

Not vulnerabilities:

- what Dev mode and Exec mode are meant to do: a bot with these permissions
  can change automations and run Lua on the live runtime. A promoted module
  runs with the full rights of the vclu process (files, processes), so Dev
  mode amounts to code execution on the host. Grant it only to bots you
  trust,
- attacks that need an admin login or shell access to the host,
- the Grenton protocol itself (vclu has to stay compatible with real CLUs),
- automations and dashboards the house's owner wrote, for example a control
  left without a PIN.

## Deployment advice

- Run vclu on a trusted local network. Do not expose the web UI, the MCP
  endpoint or the MQTT broker directly to the internet; use a VPN or the vclu
  cloud.
- Keep Dev mode off when you do not need it (it has a kill switch in the web
  UI) and give each bot its own token and access profile.
- Protect gates, doors and the alarm with a PIN when they are reachable from
  the cloud or a voice assistant.
