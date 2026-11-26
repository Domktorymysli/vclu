# Security policy

vclu controls real houses: lights, covers, heating, gates and, through Satel, the
alarm. Please report security problems privately.

## Reporting a vulnerability

Use **GitHub private vulnerability reporting**: the repository's
**Security** tab -> **Report a vulnerability**. Without a GitHub account, write
to **security@vclu.pl**. Do not open a public issue, discussion or pull request
for a security problem. Reports in English or Polish.

This is not an emergency contact. If your house is being attacked now, call
the police or your alarm monitoring company first.

Please include:

- the vclu version (`vclu --version`) and how it runs (binary, `.deb`, Docker,
  OS), or the app and its version,
- what an attacker can do and from where (the internet, the cloud, LAN,
  HA/MQTT, an MCP bot, a plugin, a Lua module),
- steps to reproduce, ideally without real hardware.

Do not include other people's personal data beyond what is needed to show the
problem. There is no bug bounty; we do not pay for reports.

## Testing

Test only your own installation, your own cloud account and your own house.
Do not:

- scan, fuzz or load-test `cloud.vclu.pl` automatically; for anything beyond
  your own account, contact us first,
- disrupt the cloud or anyone's house,
- access, keep or pass on other people's data; if you reach any by accident,
  stop, tell us what you reached, and then delete it,
- operate gates, doors, locks, the alarm or other devices in a house that is
  not yours; if you find a way to reach another user's house, stop at the
  first proof (for example a read-only request) and report it, without
  sending it any command,
- use phishing, social engineering or physical attacks.

Please do not disclose the problem before a fix or mitigation is available, or
before 90 days from your report, whichever comes first, unless we agree
otherwise.

If you make a good-faith effort to follow this policy (you test only what is
yours, you stop and report as soon as you find a problem, you do not access or
keep other people's data, you do not disrupt the cloud or anyone's house, you
give us reasonable time to fix the problem before you disclose it, and you do
not ask for payment in exchange for the details), we will not pursue legal
action against you for that research. This covers only claims of the vclu maintainer. It cannot bind public authorities,
other users, or third parties such as hosting providers, app stores or the
makers of devices vclu connects to, and it does not change obligations we have
under law, for example to report a personal data breach.

## What happens next

vclu is maintained by a single developer; there is no dedicated security
team. We aim to acknowledge a report within 3 business days and to give a
first assessment (whether it is a vulnerability, how severe, what happens
next) within 10 business days. Critical reports come first. Response times may
vary with availability and the complexity of the issue. We provide updates as
appropriate throughout the remediation process.

We decide whether a report is a vulnerability, how severe it is and whether
and how it is fixed, and we may decline reports that are out of scope.
Problems we already know about and are fixing may be closed as duplicates.

- **Fix:** a fix ships in a release; a fix for a critical problem normally
  gets a release of its own as soon as it is ready.
- **Disclosure:** we coordinate public disclosure with the availability of a
  fix or a mitigation, as a GitHub security advisory. If users need to act
  sooner to protect themselves, we may publish security guidance before a fix
  is released. Tell us if you want to be credited.

Where the EU Cyber Resilience Act (Regulation (EU) 2024/2847) applies to vclu,
we report actively exploited vulnerabilities and severe incidents as its
Article 14 requires, and inform affected users.

## Supported versions

Security fixes ship in a new release, free of charge, for the platforms that
release supports; only the latest release gets them. Upgrading is how a fix
arrives: `sudo apt upgrade` with the APT repository, a new image with Docker,
or the update script (`deploy/update.sh`) for an install made with
`install.sh`. The web UI tells you when a new version is out. Release notes
mark security fixes once they can be disclosed.

We update the cloud (`cloud.vclu.pl`) ourselves; you do not need to do
anything. The mobile apps are updated through Google Play and the App Store.

## How fixes are announced

- a **GitHub security advisory** in this repository, with a CVE when one
  applies,
- a **Security** line in the release notes and `CHANGELOG.md`,
- for a problem that needs action from users (beyond upgrading), also on
  [vclu.pl](https://vclu.pl).

## Verifying what you install

- The archives and `.deb` packages of each
  [release](https://github.com/Domktorymysli/vclu/releases) are listed with
  their SHA-256 in `checksums.txt`: `sha256sum -c checksums.txt
  --ignore-missing`. Checksums catch a damaged download, not a forged one:
  whoever could replace an asset could replace `checksums.txt` too.
- The APT repository (`apt.vclu.pl`) is signed by the maintainer; the signing
  key is never stored in CI ([deploy/apt/README.md](deploy/apt/README.md)).
- Docker images are published to `ghcr.io/domktorymysli/vclu` only by this
  repository's release workflows.
- Starting with vclu 1.13.0, every release has an SPDX SBOM of each archive,
  `.deb` package and image (`*.spdx.json` among its assets).

## Security records

Starting with vclu 1.13.0, every release keeps a record of how it was checked,
as release assets; an earlier release has only the weekly scans made while it
was the latest:

- `vclu-X.Y.Z-security.json`: the commit and tag it was built from, the Go
  version, its SBOMs with their SHA-256, and every scan of it: the date, the
  scanners and their versions (govulncheck for the Go code, Trivy for the image,
  OSV for the bundled web libraries), the result and the public advisory ids
  found. An advisory found by a scan is listed as open until we have assessed
  it, then with how it was handled: the release and the GitHub security
  advisory that fixed it, or why vclu is not affected.
- `vclu-X.Y.Z-scan-<time>-<kind>.tar.gz`: the scanners' raw output, from the
  scan before the release and from the weekly scans while it is the latest
  release.

The latest release is scanned again every week, since new advisories come out
after a release, and each scan is added to its record. Findings reviewed and
accepted as not affecting vclu are listed with the reason in
[.github/govulncheck-accepted.txt](.github/govulncheck-accepted.txt) and
[.github/scan-accepted.txt](.github/scan-accepted.txt).

The records and the raw outputs come from scanners run in GitHub Actions on
the public code and release assets against public vulnerability databases;
anyone can repeat these scans. They contain no reports about vclu itself:
those are handled privately as described above and are not written there
before they are disclosed.

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
- weaknesses in the Grenton or Satel protocols as designed by their makers;
  report those to the makers. How vclu itself handles these protocols stays in
  scope,
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

## About this policy

This policy describes how we handle security reports. It is not a warranty, a
service level or a guarantee that vclu is free of vulnerabilities. vclu is
provided as described in [LICENSE](LICENSE); the vclu cloud and the mobile apps
are provided under their own terms. We may change this policy at any time; the
version in the `main` branch applies.
