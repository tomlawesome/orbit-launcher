# Flakes

A check that failed and passed again on unchanged code. One heading per flake,
one line per sighting: date · commit · pipeline/job · symptom. The third
sighting files an issue, linked from the heading; fixing the cause deletes the
heading.

## TestDiagnose_FindsASessionDetachedDescendant (test/live)

- 2026-10-06 · 9e15161 · local, 2 of about 25 runs alongside TestLive_HangDiagnosisProducesAGoroutineDump · fails only when run with the hang-diagnosis test; always passes alone

## Unidentified internal/ui failure

- 2026-10-20 · 86a3b52 (plus comment-only edits) · local, full `go test ./internal/ui/` · failed once, output not kept; passed 6 of 6 reruns. Possibly a star drawn inside a line of text, which #270 fixed (that flake's heading was removed: 10 of 10 runs passed after the fix)
- 2026-10-21 · 8f1f1bb plus a one-line notice.go clamp · local, full `go test ./...` · failed once, output not kept; passed 2 of 2 reruns

## TestServiceStates_SlowDockerIsCutOffAndSaysSo (internal/deploy)

- 2026-10-21 · 2cd89f3 · local, full `go test ./...` on a busy host · error said "996ms" where the test wants "within 1s"; passed on rerun

## TestAppModel_UnreadableEnvScreensHoldAtEveryWidth (internal/ui)

- 2026-10-21 · 834d2c4 (fix/lows-repair) · local, full `go test ./...` · failed once, output not kept; passed 43 of 43 reruns. Possibly the long-TMPDIR wrap in #279
