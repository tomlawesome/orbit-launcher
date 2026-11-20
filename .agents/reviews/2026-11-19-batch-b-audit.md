# Audit: batch-b (#362)

Range bae061a..4d3108e (batch-b after its catch-up from dev, frozen at
bdbf30c, plus the audit fix): retiring the old SMTP lines (#339), the
launcher command on Update's dead ends (#354), Renovate's Playwright
range (#360 part 1) and the #343 notes, with dev's v0.4.6 back-merges,
codeql-action and Playwright updates.

## Passes

- Release pass: Function, Robustness and Safety per slice, with property
  passes, plus a deleted-code check and the docs pass. High and Medium go
  to the top-model ruling; Lows to the sweep.
- Security, once, on the final head: the launcher and CI and supply chain.

## Result

- No High or Medium.
- Two Lows. One at Fix-when now, a test that could not fail: fixed, fix
  review clean in one round, its one Low also fixed. One for later, on
  the post-release issue.
- Security found nothing.

Audited-through: 4d3108e77100b051b4cafa4a550caec00e17c4b1
