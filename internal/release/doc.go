// Package release holds the version/revision embedded at build time via
// -ldflags, and the self-update check against GitHub Releases.
package release

// Version and Revision are set at build time via -ldflags
// (-X github.com/tomlawesome/orbit-launcher/internal/release.Version=...).
// They default to "dev" for local, unreleased builds.
var (
	Version  = "dev"
	Revision = "unknown"
)
