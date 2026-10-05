# Contributing

This repository is **not currently accepting outside code changes.** That
is a deliberate, temporary choice. The owner holds all the rights to this
code, which lets it be offered under both a free and a commercial licence
(see [LICENSING.md](LICENSING.md)). Accepting outside code would complicate
that, so for now we don't. If that changes, this file will set out a
contributor agreement before any outside change is merged.

Bug reports and ideas are welcome as issues on the
[GitHub issue tracker](https://github.com/tomlawesome/orbit-launcher/issues).
Day-to-day work is tracked on the owner's
[GitLab](https://gitlab.tomlawson.io/ai/orbit-launcher), which is where
merge requests are made. GitHub is a public mirror of it.

## How changes are made here

- There are three protected branches: `dev` (where work comes together),
  `preview` (the step between `dev` and a stable release) and `main`
  (stable). Tags are cut from `dev`. See
  [docs/releasing.md](docs/releasing.md).
- Every change starts as a short-lived branch off `dev`, named for the
  kind of work (`feature/…`, `fix/…`, `chore/…`, `docs/…`), and is
  merged by merge request on GitLab.
- Every change needs an issue first. The owner says when a merge request
  may be merged.
