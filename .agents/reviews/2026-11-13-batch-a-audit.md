# Audit: batch-a (#350)

Range 893a610..f2f9791 (batch-a, frozen at f2f9791). dev had nothing batch-a
lacked, so no catch-up was needed.

## Passes

- Docs pass on the frozen head.
- Release pass: Function, Robustness and Safety per slice, with property
  passes, plus a moved-code pass and a deleted-code check. High and Medium
  go to the top-model ruling; Lows to the sweep.
- Security, once, on the final head: the launcher and CI and supply chain.

## Result

- No High or Medium.
- One Low at Fix-when now: a test that stopped before the step it was named
  for. Fixed in the test (!291), fix review clean in one round.
- Four Lows (Fix-when next) and the Notes are on #351.

Audited-through: 2ce8d44c75ea71526e5f62bd35f286ee6e1555a3
