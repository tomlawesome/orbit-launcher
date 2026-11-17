# Audit: batch-b (#355)

Range 32100b6..5097678 (batch-b, frozen at 5097678). batch-b already
contained dev, so no catch-up was needed.

## Passes

- Docs pass on the frozen head.
- Release pass: Function, Robustness and Safety per slice, with property
  passes, plus a deleted-code check (no moved code). High and Medium go to
  the top-model ruling; Lows to the sweep.
- Security, once, on the final head: the launcher and CI and supply chain.

## Result

- No High.
- One Medium at Fix-when now: the Remove command could not be re-run after
  it stopped at the last step (#353). Fixed, fix review clean in one round.
- One Low (Fix-when next) is #354; the Notes are recorded.
- Security found nothing to fix.

Audited-through: 52ba1fa8e42f082a1600fba4a3308ae663527e46
