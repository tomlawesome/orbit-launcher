# Changelog

All notable changes to orbit-launcher are listed here. Orbit ships the
launcher, pinned to a tag from this repository, so each entry says what
changes for people who install or run Orbit with it.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Releases before v0.4.7 are described on their GitLab release pages only.

## [Unreleased]

### Added

- A **Maintenance** row in the menu closes Orbit for maintenance or reopens
  it from the launcher, so getting back into a closed instance no longer
  depends on a hand-run script or a working sign-in. Closing takes a
  notice and an optional end time (a length like 45m or a clock time like
  22:30, server time) and confirms first; the result shows Orbit's own
  outcome, including "Maintenance was not active". It needs an Orbit
  release that ships the maintenance scripts. (#242)

### Changed

- The launcher's screens are styled with lipgloss v2.1.1, the library that
  sets their colours and layout, and its text helper ansi v0.11.10. Nothing
  on the screens changes.

## [0.4.8] - 2026-11-23

### Changed

- The launcher's screens are drawn with bubbletea v2.1.2, a bug-fix
  release of the terminal library they use. Nothing on the screens
  changes. (#361)

## [0.4.7] - 2026-11-20

### Added

- Repair can remove the old outgoing-mail lines, the SMTP password file and
  the mail overlay once Orbit keeps its own mail settings, and an update that
  removed them says so on its success screen. (#339)

### Fixed

- When the start screen says a newer launcher is out and points to Update,
  Update now shows the launcher's update command, with Copy, on its confirm
  screen and on every screen where it cannot go on, including when nothing
  is installed in this folder or Orbit runs from another folder. (#354)
