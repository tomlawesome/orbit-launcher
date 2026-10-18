# Flakes

A check that failed and passed again on unchanged code. One heading per flake,
one line per sighting: date · commit · pipeline/job · symptom. The third
sighting files an issue, linked from the heading; fixing the cause deletes the
heading.

## TestDiagnose_FindsASessionDetachedDescendant (test/live)

- 2026-10-06 · 9e15161 · local, 2 of about 25 runs alongside TestLive_HangDiagnosisProducesAGoroutineDump · fails only when run with the hang-diagnosis test; always passes alone

## TestRepairModel_TeaTest_HealthyDiagnosis (internal/ui)

- 2026-10-18 · 5c056ce · pipeline 2693 / job 37012 `fast` · WaitFor timed out at 5 s with "Diagnosis clear" already drawing; 20 of 20 local runs on the same commit pass
