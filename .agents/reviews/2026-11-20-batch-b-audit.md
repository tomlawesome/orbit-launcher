# Audit: batch-b (#367)

Range 2c8c851..6500a49 (batch-b reopened from batch-a, the Orbit pin
bump, and its catch-up from dev and preview).

## Result

- No hand-written code since the last audit. orbit/pin.json is
  tool-written: it matches scripts/bump-orbit-pin.sh preview and
  Orbit's preview tip. The other changes are catch-up merges whose
  content dev already has.
- No findings.

Audited-through: 6500a492cedbc86e21d3c984f3981db6fd766d98
