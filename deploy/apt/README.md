# APT repository

Every release attaches `vclu_<version>_{amd64,arm64,armhf}.deb` (GoReleaser `nfpms`, maintainer
scripts in `deploy/deb/`). The repository that serves them is static files signed by the
maintainer: the key stays on the maintainer's machine, CI never signs.

## Users

```bash
sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://apt.vclu.pl/vclu.gpg | sudo tee /etc/apt/keyrings/vclu.gpg >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/vclu.gpg] https://apt.vclu.pl stable main" \
  | sudo tee /etc/apt/sources.list.d/vclu.list
sudo apt update && sudo apt install vclu
```

Then `sudo apt upgrade` updates vCLU with the rest of the system.

The package uses the install.sh layout (`/opt/vclu`, data in `/opt/vclu/data`, port 8080), so it
also updates an install.sh setup in place: the unit install.sh wrote in `/etc/systemd/system`
overrides the packaged one and keeps its directories and port. Before every upgrade the
configuration is backed up to `/opt/vclu/backup-<old version>-<date>.tar.gz` (three kept), as
update.sh does. Removing or purging the package never deletes data.

## Maintainer

Once: a signing key (with a passphrase) and a web server serving a directory as
`https://apt.vclu.pl/`.

```bash
gpg --quick-generate-key "vCLU packages" ed25519 sign 5y
gpg --list-keys --keyid-format long "vCLU packages"    # the id for APT_KEY
```

Keep a backup of the key (`gpg --export-secret-keys --armor <id>`) offline: losing it means every
user has to install a new key.

After each release:

```bash
APT_KEY=<id> APT_DEST=user@host:/srv/apt.vclu.pl deploy/apt/publish.sh v1.10.0
```

It downloads the release's packages, keeps the last five per architecture, builds the indexes
(apt-ftparchive in a container), signs them and uploads packages before indexes. `SUITE=beta`
publishes to a separate `beta` suite; `--no-upload` only builds the local copy in `~/vclu-apt`.
