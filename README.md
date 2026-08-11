# orbit-launcher

A dedicated terminal application for installing, updating, and repairing
Orbit — full-screen, animated starfield background, static Orbit mark,
and a small set of clear choices.

This supersedes the bash-script "command centre" work from
[orbit#260](https://github.com/tomlawesome/orbit/issues/260). That
approach dressed up `install.sh` with more terminal control codes;
orbit-launcher is a proper TUI application instead.

## Status

Early development (Wave 0/1 of [`docs/implementation-plan.md`](docs/implementation-plan.md)).
See [`design/mockups.html`](design/mockups.html) for the style guide and
screen-by-screen layout mockups (open it in a browser).

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
