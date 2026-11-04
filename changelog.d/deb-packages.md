### Installation

- **Debian packages** - every release attaches `vclu_<version>_{amd64,arm64,armhf}.deb`. The package uses the install.sh layout, so it also updates an existing install.sh setup in place, keeping its directories and port; before each upgrade the configuration is backed up to `/opt/vclu/backup-*.tar.gz`, and removing the package never deletes data. An APT repository for `apt upgrade` is described in `deploy/apt/README.md`.
