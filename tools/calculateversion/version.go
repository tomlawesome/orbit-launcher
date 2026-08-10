// Package main calculates orbit-launcher's next release version: one
// semantic version per release train, read from the highest stable
// vMAJOR.MINOR.PATCH git tag. An ordinary preview train increments minor
// and resets patch; a hotfix train increments patch. Before the first
// stable tag exists, the baseline is 0.1.0. Ported from the same rule
// orbit's own docs/releasing.md documents for its container-digest model,
// adapted to a binary-release repository with no version history yet.
package main

import (
	"fmt"
	"regexp"
	"sort"
)

// Version is a parsed vMAJOR.MINOR.PATCH tag.
type Version struct {
	Major, Minor, Patch int
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v Version) less(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

var stableTagPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// ParseStableTag parses a "vMAJOR.MINOR.PATCH" tag. It returns ok=false for
// anything else (pre-release suffixes, "preview", "latest", malformed tags)
// — only exact stable tags participate in version calculation.
func ParseStableTag(tag string) (v Version, ok bool) {
	match := stableTagPattern.FindStringSubmatch(tag)
	if match == nil {
		return Version{}, false
	}
	var maj, min, pat int
	if _, err := fmt.Sscanf(match[1], "%d", &maj); err != nil {
		return Version{}, false
	}
	if _, err := fmt.Sscanf(match[2], "%d", &min); err != nil {
		return Version{}, false
	}
	if _, err := fmt.Sscanf(match[3], "%d", &pat); err != nil {
		return Version{}, false
	}
	return Version{Major: maj, Minor: min, Patch: pat}, true
}

// HighestStable returns the highest stable version among tags, and whether
// any stable tag was found at all.
func HighestStable(tags []string) (Version, bool) {
	var versions []Version
	for _, tag := range tags {
		if v, ok := ParseStableTag(tag); ok {
			versions = append(versions, v)
		}
	}
	if len(versions) == 0 {
		return Version{}, false
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].less(versions[j]) })
	return versions[len(versions)-1], true
}

// baselineVersion is the calculated version before any stable tag exists.
var baselineVersion = Version{Major: 0, Minor: 1, Patch: 0}

// NextVersion calculates the next release train's version from the set of
// existing tags. hotfix increments patch instead of minor.
func NextVersion(tags []string, hotfix bool) Version {
	highest, found := HighestStable(tags)
	if !found {
		return baselineVersion
	}
	if hotfix {
		return Version{Major: highest.Major, Minor: highest.Minor, Patch: highest.Patch + 1}
	}
	return Version{Major: highest.Major, Minor: highest.Minor + 1, Patch: 0}
}
