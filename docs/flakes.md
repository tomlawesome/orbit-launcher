# Flakes

A check that failed and passed again on unchanged code. One heading per flake,
one line per sighting: date · commit · pipeline/job · symptom. The third
sighting files an issue, linked from the heading; fixing the cause deletes the
heading. A heading names the test as `go test -run` matches it: the third
sighting leaves the gate by that name.

## TestDiagnose_FindsASessionDetachedDescendant (test/live)

- 2026-10-06 · 9e15161 (no longer in history) · local, 2 of about 25 runs alongside TestLive_HangDiagnosisProducesAGoroutineDump · fails only when run with the hang-diagnosis test; always passes alone

## Unidentified internal/ui failure

- 2026-10-20 · 86a3b52 (plus comment-only edits) · local, full `go test ./internal/ui/` · failed once, output not kept; passed 6 of 6 reruns. Possibly a star drawn inside a line of text, which #270 fixed (that flake's heading was removed: 10 of 10 runs passed after the fix)
- 2026-10-21 · 8f1f1bb plus a one-line notice.go clamp · local, full `go test ./...` · failed once, output not kept; passed 2 of 2 reruns

## TestConsole_RealPTY_ClosingTheTerminalMidInstallLetsTheInstallerRollBack (test/pty)

- 2026-10-22 · c4432e7 · pipeline 2896 / fast (`-race`) · the launcher exited 1.1 s after the terminal closed with exit status 1; the installer's EXIT trap left no marker and staged files stayed in TMPDIR. Passed 5 of 5 local `-race` runs on the same commit and 5 of 5 on `dev` aad8058
- 2026-10-28 · bf0bc57 (Go code as 4f4f0b5; only .gitlab-ci.yml changed) · local, full `go test ./... -count=1` (no `-race`) · same symptom: exited 1.0 s after the terminal closed with exit status 1, no rollback marker, staged files left in TMPDIR. Passed alone on rerun

## TestConsole_RealPTY_QuittingDuringTheConfigurationImportLeavesNothingInTMPDIR (test/pty), #305

- 2026-10-28 · 7e47960 · pipeline 3115 / fast job 40994 (`-race`) · SIGTERM round 3: the import reached 3572 of 3901 secrets inside the 30 s setup wait (subtest took 51.5 s); the quits themselves exited in 17–287 ms and left nothing in TMPDIR. Passed 3 of 3 local `-race` runs on the same commit (subtest about 4 s)
- 2026-10-28 · 7e47960 · pipeline 3115 / fast job 41018 (retry, `-race`) · same symptom at round 5: 6284 of 7801 secrets within 30 s; quits exited in 4–214 ms and left nothing. Filed as #305: it blocks !240, so it is not left for a third sighting

## TestLive_InstallHealthyEndpointThenRemove/Install (test/live)

- 2026-10-23 · 0b6bacf · pipeline 2956 / live job 39207 · install.sh stopped at the OIDC step with "OIDC provider is unavailable" after reading accounts.google.com's discovery document; the same code passed in 2954, and the retry 39208 passed
