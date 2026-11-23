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

## Testing

Test only your own installation, your own cloud account and your own house.
Do not:

- run destructive tests or degrade the cloud for other users,
- access, keep or pass on other people's data; if you reach any by accident,
  stop, delete it and tell us,
- operate gates, doors, locks, the alarm or other devices in a house that is
  not yours.

If you act in good faith and follow this policy, we will not take legal
action against you over that research. We can speak only for vclu, not for
third parties such as the makers of devices vclu connects to.

## What happens next

vclu is maintained by a single developer; there is no dedicated security
team. We aim to acknowledge a report within 3 business days and to
give a first assessment (whether it is a vulnerability, how severe, what
happens next) within 10 business days. Critical reports come first. Response
times may vary with availability and the complexity of the issue. We provide
updates as appropriate throughout the remediation process.

- **Fix:** a fix ships in a release; a critical one gets a release of its own
  as soon as the fix is ready.
- **Disclosure:** we coordinate public disclosure with the availability of a
  fix or a mitigation, as a GitHub security advisory. If users need to act
  sooner to protect themselves, we may publish security guidance before a fix
  is released. Tell us if you want to be credited.

Where the EU Cyber Resilience Act (Regulation 2024/2847) applies, an actively
exploited vulnerability and a severe incident affecting the security of vclu
are reported as its Article 14 requires, through the single reporting platform
of ENISA, and users are told what to do.

## Supported versions

Security fixes ship in a new release; only the latest release gets them.
Upgrades are free, so upgrading is how a fix arrives: `sudo apt upgrade` with
the APT repository, a new image with Docker, **Update** in the web UI
otherwise. Each release says in its notes whether it fixes a security problem.

The cloud (`cloud.vclu.pl`) is always on its latest version. The mobile apps
are updated through Google Play and the App Store.

## How fixes are announced

- a **GitHub security advisory** in this repository, with a CVE when one
  applies,
- a **Security** line in the release notes and `CHANGELOG.md`,
- for a problem that needs action from users (beyond upgrading), also on
  [vclu.pl](https://vclu.pl).

## Verifying what you install

- Release assets are listed with their SHA-256 in `checksums.txt` of each
  [release](https://github.com/Domktorymysli/vclu/releases):
  `sha256sum -c checksums.txt --ignore-missing`. Checksums catch a damaged
  download, not a forged one: whoever could replace an asset could replace
  `checksums.txt` too.
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

The records hold only what anyone can already find: the scanners check the
public code and release assets against published advisories for vclu's
dependencies, and anyone can repeat those scans. A vulnerability in vclu itself
is never written there before it is disclosed; it is handled privately as
described above.

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
- the cloud (accounts, pairing a vclu, the tunnel to a house, remote
  dashboards): reaching or controlling a house that is not yours, or one that
  was not shared with you,
- the mobile apps: their sessions and tokens, notifications and actions,
- a crafted message from a Grenton CLU, a Satel module or another device that
  makes vclu crash, run code or act outside its configuration.

Official plugins (`@vclu/*`) are in scope. Community plugins belong to their
authors: report to them; if a plugin can harm users beyond its own purpose,
tell us too.

Not vulnerabilities:

- actions within the permissions an account, bot or plugin was explicitly
  given. Escalating privileges, crossing an authorization boundary (for
  example reaching another account's house) or escaping a sandbox stays in
  scope, even when it needs a logged-in account,
- what Dev mode and Exec mode are meant to do: a bot with these permissions
  can change automations and run Lua on the live runtime. A promoted module
  runs with the full rights of the vclu process (files, processes), so Dev
  mode amounts to code execution on the host. Grant it only to bots you
  trust,
- attacks that need shell access to the host,
- weaknesses of the Grenton and Satel protocols themselves (vclu has to stay
  compatible with real devices),
- automations and dashboards the house's owner wrote, for example a control
  left without a PIN.

## Deployment advice

- Run vclu on a trusted local network. Do not expose the web UI, the MCP
  endpoint or the MQTT broker directly to the internet; use a VPN or the vclu
  cloud.
- Keep Dev mode off when you do not need it (it has a kill switch in the web
  UI) and give each bot its own token and access profile.
- Protect gates, doors and the alarm with a PIN when they are reachable from
  the cloud.
