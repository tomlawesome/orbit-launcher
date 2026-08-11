# orbit-launcher

A dedicated terminal application for installing, updating, and repairing
Orbit — full-screen, animated starfield background, static Orbit mark,
and a small set of clear choices.

This supersedes the bash-script "command centre" work from
[orbit#260](https://github.com/tomlawesome/orbit/issues/260). That
approach dressed up `install.sh` with more terminal control codes;
orbit-launcher is a proper TUI application instead.

## Quickstart

No stable release has shipped yet (see [`docs/releasing.md`](docs/releasing.md)
for what that means and how one gets there), so the bootstrap script
isn't on `main` yet either — it only exists on `develop`/`preview` so
far. Until v1.0.0, fetch it from `develop` and pin to the current
preview build explicitly:

```
ORBIT_LAUNCHER_VERSION=preview-latest curl -fsSL https://raw.githubusercontent.com/tomlawesome/orbit-launcher/develop/scripts/get-orbit-launcher.sh | bash
```

Once v1.0.0 ships, the permanent quickstart becomes:

```
curl -fsSL https://raw.githubusercontent.com/tomlawesome/orbit-launcher/main/scripts/get-orbit-launcher.sh | bash
```

Either way, this downloads the right binary for your machine (amd64 or
arm64), verifies its checksum, and runs it — nothing else is installed.
Run it again any time to re-launch; it caches the binary at
`~/.cache/orbit-launcher`. From the menu: **Install** deploys Orbit for
the first time, **Update** pulls the latest image into an existing
deployment, **Remove** stands the containers down, **Repair** isn't
built yet.

On launch, orbit-launcher makes one non-blocking check against GitHub
for a newer stable release, showing a small notice on the splash
screen if one exists — it never fetches or changes anything itself, it
just tells you. Set `ORBIT_LAUNCHER_NO_UPDATE_CHECK=1` to disable it.

## Status

Early development (Wave 0-3 of [`docs/implementation-plan.md`](docs/implementation-plan.md)):
Install, Update and Remove are wired to a real `install.sh`; Repair is a
deliberate stub. See [`design/mockups.html`](design/mockups.html) for
the style guide and screen-by-screen layout mockups (open it in a
browser).

## Stack

Go, using [`charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea)
for the full-screen event loop and
[`charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss)
for layout and styling. Linux only (Debian, Ubuntu and similar) — this
runs on the server being managed, not as a cross-platform desktop tool.

## Licence

[AGPL-3.0](LICENSE), with a commercial license available for uses that
don't fit those terms — see [`LICENSING.md`](LICENSING.md).

## Contributing

Not currently accepting external pull requests — see
[`CONTRIBUTING.md`](CONTRIBUTING.md).
