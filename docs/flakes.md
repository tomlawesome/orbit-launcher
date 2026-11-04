# Flakes

A check that failed and passed again on unchanged code. One heading per flake,
one line per sighting: date · commit · pipeline/job · symptom. The third
sighting files an issue, linked from the heading; fixing the cause deletes the
heading. A heading names the test as `go test -run` matches it: the third
sighting leaves the gate by that name. A failure no test can be named for yet
is logged under a plain heading; at its third sighting, reproduce it with the
output kept (`go test -count=20 -v` on the package) and rename the heading to
the test, or, if it will not reproduce, move its sightings into the issue and
delete the heading.

## TestDiagnose_FindsASessionDetachedDescendant (test/live)

- 2026-10-06 · 9e15161 (no longer in history) · local, 2 of about 25 runs alongside TestLive_HangDiagnosisProducesAGoroutineDump · fails only when run with the hang-diagnosis test; always passes alone

## Unidentified internal/ui failure

- 2026-10-20 · 86a3b52 (plus comment-only edits) · local, full `go test ./internal/ui/` · failed once, output not kept; passed 6 of 6 reruns. Possibly a star drawn inside a line of text, which #270 fixed (that flake's heading was removed: 10 of 10 runs passed after the fix)
- 2026-10-21 · 8f1f1bb plus a one-line notice.go clamp · local, full `go test ./...` · failed once, output not kept; passed 2 of 2 reruns

## TestLive_InstallHealthyEndpointThenRemove/Install (test/live)

- 2026-10-23 · 0b6bacf · pipeline 2956 / live job 39207 · install.sh stopped at the OIDC step with "OIDC provider is unavailable" after reading accounts.google.com's discovery document; the same code passed in 2954, and the retry 39208 passed

## TestLive_UpdateReconfiguresOnTheNewImage/Update (test/live) (host)

- 2026-11-03 · 9f4d75c · pipeline 3487 / live job 44764 · the update stopped at "Resolving image identity": Docker's request to the Orbit image registry got "connection refused". The Fedora leg pulled the same image in the same pipeline, and the retry 44766 passed
