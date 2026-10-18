package release

import "strings"

// DisplayVersion is how a launcher version reads on screen: always
// v-prefixed, and an unreleased build ("" or "dev") reads "dev" rather
// than passing for a release. Release owns this shape (#199 EN-13); the
// UI prints what it is given and never invents a version of its own.
func DisplayVersion(v string) string {
	if v == "" || v == "dev" {
		return "dev"
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}
