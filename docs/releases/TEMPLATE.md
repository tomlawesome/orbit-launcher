# Release record template

Copy this file to `docs/releases/vX.Y.Z.md` before step 1 of
[docs/releasing.md](../releasing.md), replace every `<...>`, and commit it on
a `release/vX.Y.Z` branch into `dev` with
`git commit --trailer "Signed-off-by: <your name>"` (a name only, no email;
`git commit -s` would add one). See "The signed release record" in the guide
for what each line must say, and keep one `step N:` line per numbered step of
"What you do, in short", in the guide's order. A step says one of:

- `done: <link or job id>`
- `not-needed: <the owner decision that says so>`
- `after-merge` (the step comes after this promotion)

An empty value or a `<...>` placeholder fails the check
(`go run ./tools/releasereadiness`).

---

version: vX.Y.Z
guide-blob: <git rev-parse HEAD:docs/releasing.md>
guide-commit: <git log -1 --format=%H -- docs/releasing.md>

step 1: after-merge
step 2: after-merge
step 3: after-merge
step 4: after-merge
step 5: after-merge
step 6: after-merge

Signed-off-by: <your name, as git config user.name>
