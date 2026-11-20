# Changelog

All notable changes to orbit-launcher are listed here. Orbit ships the
launcher, pinned to a tag from this repository, so each entry says what
changes for people who install or run Orbit with it.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Releases before v0.4.7 are described on their GitLab release pages only.

## [Unreleased]

### Added

- Repair can remove the old outgoing-mail lines, the SMTP password file and
  the mail overlay once Orbit keeps its own mail settings, and an update that
  removed them says so on its success screen. (#339)

### Fixed

- When the start screen says a newer launcher is out and points to Update,
  Update now shows the launcher's update command, with Copy, on its confirm
  screen and on every screen where it cannot go on, including when nothing
  is installed in this folder or Orbit runs from another folder. (#354)
