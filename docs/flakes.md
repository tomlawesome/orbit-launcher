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

## TestConsole_RealPTY_ClosingTheTerminalMidInstallLetsTheInstallerRollBack (test/pty)

- 2026-10-22 · c4432e7 · pipeline 2896 / fast (`-race`) · the launcher exited 1.1 s after the terminal closed with exit status 1; the installer's EXIT trap left no marker and staged files stayed in TMPDIR. Passed 5 of 5 local `-race` runs on the same commit and 5 of 5 on `dev` aad8058
