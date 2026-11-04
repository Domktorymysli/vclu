# APT repository

Every release attaches `vclu_<version>_{amd64,arm64,armhf}.deb` (GoReleaser `nfpms`, maintainer
scripts in `deploy/deb/`). The repository that serves them is static files signed by the
maintainer; CI never signs.

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

## Publishing

The maintainer publishes each release to `https://apt.vclu.pl` after it is out: the packages from
the GitHub release, indexes signed with the repository key (kept offline, never in CI), the last
five versions per architecture. `stable` is the only suite for now.
