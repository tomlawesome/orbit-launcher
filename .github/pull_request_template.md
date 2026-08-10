<!--
Every pull request must end with exactly one Observability-Impact
declaration. If it changes a protected planning path (see
.github/planning-governance.json), it must also include a Planning-Model
attestation line. Both are enforced by CI ("Static and unit checks").
-->

## Summary

## Observability-Impact

Pick exactly one:

- `Observability-Impact: changed` — then also fill in all four:
  - Operational event/state:
  - Failure/recovery:
  - Privacy/redaction:
  - Operator-documentation impact:
- `Observability-Impact: none — <specific reason>`

## Planning-Model

Only required if this PR touches a protected planning path
(`.github/workflows/`, `docs/adr/`, or a file listed in
`.github/planning-governance.json`):

`Planning-Model: Human`
