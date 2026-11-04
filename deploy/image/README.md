# Raspberry Pi image

`build-image.sh <tag>` turns the official Raspberry Pi OS Lite (arm64: Pi 3, 4, 5, CM4, CM5) into
`vclu-<version>-rpi-arm64.img.xz`. The release's arm64 `.deb` (the archive for releases without
one) goes into the root partition with a one-shot unit; on first boot it installs it, removes
itself and vCLU opens the setup wizard at `http://vclu.local:8080` (or the hostname set in
Raspberry Pi Imager). Installed from the `.deb`, vCLU is upgraded with `apt` once the APT
repository is added (`deploy/apt/README.md`).

The partition is edited as a file with `debugfs`: no root, loop devices or arm64 emulation, so it
runs on a stock GitHub runner in a few minutes. `.github/workflows/rpi-image.yml` builds it for
every release and attaches it to the release.

Flash with Raspberry Pi Imager ("Use custom"). Its settings (user, SSH key, Wi-Fi, hostname) still
apply: they arrive through cloud-init and the installer runs after its config stage.

## Not yet

This is the first step. Still open:

- the image is stock Raspberry Pi OS plus vCLU: no read-only root, no A/B updates (RAUC), SSH as
  Imager sets it;
- vCLU runs as root (`deploy/vclu.service`); the image should get the hardened unit first;
- until the wizard sets a password, anyone on the LAN can open vCLU (Lua with os/io, as root);
  the image makes that window predictable, so it needs a one-time setup token;
- the base image is "latest" at build time and its checksum is checked, but not pinned per release;
- an entry in a Raspberry Pi Imager repository list, so vCLU shows up in its menu.
