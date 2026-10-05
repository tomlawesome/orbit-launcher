#!/usr/bin/env bash
# Points anyone still using this script's old URL at Orbit's installer.
#
# This repo no longer publishes launcher binaries (#171, ai/orbit#1107
# ADR-0031): Orbit builds, signs and ships the launcher alongside itself.
# The script used to download a release from here; with no releases left
# to download, all it does now is say where to go instead. Developers
# build the launcher from source (`go build ./cmd/orbit-launcher`).
#
# Usage: curl -fsSL <raw-url>/scripts/get-orbit-launcher.sh | bash
set -Eeuo pipefail

cat >&2 <<'MSG'
get-orbit-launcher: orbit-launcher no longer publishes its own releases —
Orbit builds, signs and ships it. To install Orbit (and the matching
launcher), run Orbit's installer instead:

  curl -fsSL https://raw.githubusercontent.com/tomlawesome/orbit/main/scripts/get-orbit.sh | bash
MSG
exit 1
