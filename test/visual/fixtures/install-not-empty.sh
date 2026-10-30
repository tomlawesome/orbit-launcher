#!/usr/bin/env bash
# Stand-in install.sh for the stopped-screen snapshot (#257): it refuses a
# folder that is not empty, as Orbit's installer does, before writing
# anything -- the host refusal event, the installer's own lines on stderr,
# exit 1. It ignores its arguments.
echo "phase=host component=host state=failed reason=docker-host action=retry elapsed=0s"
echo "Orbit installer: The installation directory is not empty and is not a recognizable Orbit deployment or safe pre-provisioned bootstrap." >&2
echo "Orbit installer: Refusing to install here." >&2
exit 1
