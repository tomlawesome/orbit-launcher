# Audit: batch-b (#370)

Range b488fae..0659700: the v0.4.8 work on batch-b (#361, #363, #364,
#365, #368, #369), the release guide and the flake log, about 800
hand-written lines, plus the docs fix the audit made.

## Result

- Stages run: Function, Robustness, Safety, docs pass, deleted-code
  check, then Security alone on the final head.
- No High or Medium findings. One Low and two Security findings were
  refuted; three Notes are left as they are.
- One docs fix (docs/releasing.md), checked against the CI job.

Audited-through: 0659700a5ac6cf6d6f3b83fbb0a7088ed01bb8d1
