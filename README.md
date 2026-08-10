# orbit-launcher

A dedicated terminal application for installing, updating, and repairing
Orbit — full-screen, animated starfield background, static Orbit mark,
and a small set of clear choices.

This supersedes the bash-script "command centre" work from
[orbit#260](https://github.com/tomlawesome/orbit/issues/260). That
approach dressed up `install.sh` with more terminal control codes;
orbit-launcher is a proper TUI application instead.

## Status

Design phase. See [`design/mockups.html`](design/mockups.html) for the
current style guide and screen-by-screen layout mockups (open it in a
browser).

## Planned stack

Go, using [`charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea)
for the full-screen event loop and
[`charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss)
for layout and styling.
