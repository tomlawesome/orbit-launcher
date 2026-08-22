# Contributing

This repository is **not currently accepting external pull requests.**
That's a deliberate, temporary choice, not a policy about the project's
long-term openness — it keeps the dual AGPL-3.0/commercial licensing model
(see [LICENSING.md](LICENSING.md)) legally simple while the project is
young: sole authorship means no Contributor License Agreement is needed
yet. If that changes, this file will be updated with a CLA process before
any external PR is merged.

Issues, bug reports and feature discussion are welcome on the
[issue tracker](https://github.com/tomlawesome/orbit-launcher/issues).

## Internal workflow (for reference)

- Three protected branches: `dev` (integration), `preview` (release
  lane), `main` (stable). See
  [docs/implementation-plan.md](docs/implementation-plan.md) section 4.
- Every change starts as a short-lived `codex/issue-<n>-<slug>` branch off
  `dev`, opened as a pull request that closes its tracking issue.
- No work happens on a wave or slice without a filed GitHub issue to track
  it first.
- Every pull request links its issue and is merged only on explicit owner
  direction — see `docs/implementation-plan.md` §4.1 and §4.4.
