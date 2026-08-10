// Package deploy orchestrates Docker/Compose: profile and compose-file
// selection, health probes, and stand-down. Owns every side effect on the
// host so internal/ui only ever owns rendering and input.
package deploy
