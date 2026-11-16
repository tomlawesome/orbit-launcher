# Audit: batch-a (#358)

Range 9810614..278fcbf (batch-a after its catch-up from dev, frozen at
278fcbf): the Renovate checksum-lookup fix (#356), the Orbit pin move to
preview 0d522cec and Repair's words for what it adds (#357).

## Passes

- Release pass: Function, Robustness and Safety per slice, with property
  passes, plus a deleted-code check. High and Medium go to the top-model
  ruling; Lows to the sweep.
- Security, once, on the final head: the launcher and CI and supply chain.

## Result

- No High or Medium.
- One Low at Fix-when now: a test that could not fail. Fixed, fix review
  clean in one round.
- Security found nothing.

Audited-through: bae061a0cb872124ea38818f5a881145ab9412a7
