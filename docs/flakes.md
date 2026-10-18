# Flakes

A check that failed and passed again on unchanged code. One heading per flake,
one line per sighting: date · commit · pipeline/job · symptom. The third
sighting files an issue, linked from the heading; fixing the cause deletes the
heading.

## TestDiagnose_FindsASessionDetachedDescendant (test/live)

- 2026-10-06 · 9e15161 · local, 2 of about 25 runs alongside TestLive_HangDiagnosisProducesAGoroutineDump · fails only when run with the hang-diagnosis test; always passes alone

## TestAppModel_UnreadableEnvScreensHoldAtEveryWidth (internal/ui)

- 2026-10-18 · 62a7419 · local, full `go test ./internal/ui/` · a background star drawn between "permission" and "denied" broke the text check; passed 3 of 3 alone
