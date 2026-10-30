// Every job image is pinned by content digest (#303 C3). A tag alone can be
// repointed by whoever controls the publishing account, and the job then
// runs their image with the checkout and the job's credentials; a digest
// reference makes the runner refuse anything else (GitLab docs, "Use
// checksum to keep your image secure"). Images routed through the shared
// pull-through cache are named `$CI_IMAGE_CACHE_<X>/...`, which Renovate's
// gitlabci manager can read only through renovate.json's registryAliases,
// so every prefix in use needs one, or Renovate never proposes a move for
// that image and the pin goes stale.
package supplychain

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

var digestRef = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

// upstreamOfCachePrefix is the registry each cache prefix stands for.
var upstreamOfCachePrefix = map[string]string{
	"$CI_IMAGE_CACHE_GHCR":      "ghcr.io",
	"$CI_IMAGE_CACHE_DOCKERHUB": "docker.io",
	"$CI_IMAGE_CACHE_MCR":       "mcr.microsoft.com",
}

// jobImageNames returns every non-hidden job's effective image name (after
// `extends:`), keyed by job, plus every services entry's name.
func jobImageNames(t *testing.T, doc *yamlDoc) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	nameOf := func(n *yamlNode) string {
		switch {
		case n == nil:
			return ""
		case n.kind == 's':
			return n.str
		case n.kind == 'm' && n.vals["name"] != nil:
			return n.vals["name"].str
		}
		return ""
	}
	for _, k := range doc.root.keys {
		switch k {
		case "stages", "workflow", "default", "variables", "include":
			continue
		}
		if strings.HasPrefix(k, ".") || doc.root.vals[k].kind != 'm' {
			continue
		}
		merged, err := doc.extendsMerge(doc.root.vals[k], 0)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		out[k] = append(out[k], nameOf(merged.vals["image"]))
		if svc := merged.vals["services"]; svc != nil && svc.kind == 'q' {
			for _, s := range svc.items {
				out[k] = append(out[k], nameOf(s))
			}
		}
	}
	return out
}

func TestEveryJobImageIsDigestPinned(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	if def := doc.root.vals["default"]; def != nil && def.kind == 'm' && def.vals["image"] != nil {
		t.Error("`default:` sets an image; every job names its own, pinned by digest (#303)")
	}
	vars := doc.root.vals["variables"]
	jobs := 0
	for job, names := range jobImageNames(t, doc) {
		jobs++
		image := names[0]
		if image == "" {
			t.Errorf("job %q has no image of its own (#303)", job)
			continue
		}
		resolved := image
		if strings.HasPrefix(image, "$") && !strings.Contains(image, "/") {
			v := vars.vals[strings.TrimPrefix(image, "$")]
			if v == nil {
				t.Errorf("job %q runs %s, which is not a top-level variable (#303)", job, image)
				continue
			}
			resolved = v.str
		}
		if !digestRef.MatchString(resolved) {
			t.Errorf("job %q runs %s (%s), which is not pinned by @sha256 digest: a moved tag would run someone else's image (#303)", job, image, resolved)
		}
	}
	if jobs < 9 {
		t.Fatalf("checked only %d jobs: the reader is not finding them", jobs)
	}
}

func TestImageCachePrefixesHaveRenovateAliases(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	var cfg struct {
		RegistryAliases map[string]string `json:"registryAliases"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "renovate.json")), &cfg); err != nil {
		t.Fatalf("parsing renovate.json: %v", err)
	}
	prefix := regexp.MustCompile(`^\$CI_IMAGE_CACHE_[A-Z0-9_]+`)
	used := 0
	for job, names := range jobImageNames(t, doc) {
		for _, name := range names {
			p := prefix.FindString(name)
			if p == "" {
				continue
			}
			used++
			want, known := upstreamOfCachePrefix[p]
			if !known {
				t.Errorf("job %q pulls through %s, which this test does not know: add its upstream registry to upstreamOfCachePrefix and to renovate.json's registryAliases (#303)", job, p)
				continue
			}
			if got := cfg.RegistryAliases[p]; got != want {
				t.Errorf("job %q pulls %s, but renovate.json's registryAliases maps %s to %q, want %q: without it Renovate cannot read the image and never moves its pin (#303)", job, name, p, got, want)
			}
		}
	}
	if used == 0 {
		t.Fatal("no job pulls through $CI_IMAGE_CACHE_*: private-details does, so the reader is losing it")
	}
	for alias, host := range cfg.RegistryAliases {
		if want, known := upstreamOfCachePrefix[alias]; known && host != want {
			t.Errorf("renovate.json's registryAliases maps %s to %q, want %q (#303)", alias, host, want)
		}
	}
}

// The private-details image and the gitleaks job's tarball are the same
// gitleaks release, the way TestGoPinsAgree ties GO_IMAGE to GO_VERSION
// (#179). Renovate moves the image; GITLEAKS_VERSION and GITLEAKS_SHA256 are
// moved by hand, because Renovate cannot derive the tarball's checksum.
func TestPrivateDetailsImageMatchesGitleaksVersion(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	names := jobImageNames(t, doc)["private-details"]
	if len(names) == 0 || names[0] == "" {
		t.Fatal("the private-details job names no image")
	}
	m := regexp.MustCompile(`/gitleaks/gitleaks:([^@]+)(@|$)`).FindStringSubmatch(names[0])
	if m == nil {
		t.Fatalf("the private-details image %s is not gitleaks/gitleaks:<tag>", names[0])
	}
	if want := "v" + gitlabPin(t, "GITLEAKS_VERSION"); m[1] != want {
		t.Errorf("the private-details image is gitleaks %s but GITLEAKS_VERSION says %s: move GITLEAKS_VERSION and GITLEAKS_SHA256 by hand to the image's release, or the image back (#303)", m[1], want)
	}
}
