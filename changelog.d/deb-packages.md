### Installation

- **Debian packages** - every release attaches `vclu_<version>_{amd64,arm64,armhf}.deb`. The package uses the install.sh layout, so it also updates an install.sh setup in `/opt/vclu` (the default) in place, keeping its data directory and port; before each upgrade the configuration is backed up to `/opt/vclu/backup-*.tar.gz`, a service that was stopped or disabled stays so, and removing the package never deletes data. With the APT repository (`deploy/apt/README.md`) vCLU updates with `apt upgrade`.
