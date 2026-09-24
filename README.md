# orbit-launcher

A dedicated terminal application for installing, updating, and repairing
Orbit — full-screen, animated starfield background, static Orbit mark,
and a small set of clear choices.

This supersedes the bash-script "command centre" work from
[orbit#260](https://github.com/tomlawesome/orbit/issues/260). That
approach dressed up `install.sh` with more terminal control codes;
orbit-launcher is a proper TUI application instead.

## Quickstart

orbit-launcher isn't installed from this repo. Orbit builds, signs and
ships it alongside itself, so the way to get both is Orbit's own
installer:

```
curl -fsSL https://raw.githubusercontent.com/tomlawesome/orbit/main/scripts/get-orbit.sh | bash
```

This repo keeps orbit-launcher's source, version tags and CI tests (see
[`docs/releasing.md`](docs/releasing.md)) — it doesn't publish a
downloadable binary of its own. `scripts/get-orbit-launcher.sh` here is
for orbit-launcher developers only (`ORBIT_LAUNCHER_DEVELOPER=1`).

Once installed, run orbit-launcher again any time to re-launch it. From
the menu: **Install** deploys Orbit for the first time, **Update** pulls
the latest image into an existing deployment, **Remove** stands the
containers down, **Repair** isn't built yet.

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
