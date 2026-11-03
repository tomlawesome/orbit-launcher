// ORBIT_READ_TOKEN is scoped to the `orbit-reads` environment, so only a job
// that names that environment receives it (the comment above live-prepare in
// .gitlab-ci.yml; #303). A second job naming it, directly or through `extends:`
// or through `default:`, would be handed a token that reads the private
// ai/orbit project, and no other test would notice: this one counts them.
package supplychain

import (
	"reflect"
	"strings"
	"testing"
)

// orbitReadsJobs returns, in file order, the jobs whose effective
// `environment:` is named orbit-reads, written as a mapping with a `name:` or
// as the bare name.
func orbitReadsJobs(t *testing.T, doc *yamlDoc) []string {
	t.Helper()
	reserved := map[string]bool{"include": true, "stages": true, "workflow": true, "default": true, "variables": true}
	var out []string
	for _, name := range doc.root.keys {
		if reserved[name] || strings.HasPrefix(name, ".") {
			continue
		}
		eff, err := doc.effectiveJob(name)
		if err != nil {
			t.Fatalf("reading job %s: %v", name, err)
		}
		env := eff.Keys["environment"]
		if env == nil {
			continue
		}
		got := ""
		switch env.kind {
		case 's':
			got = env.str
		case 'm':
			if n := env.vals["name"]; n != nil {
				got = n.str
			}
		}
		if strings.Trim(got, `"'`) == "orbit-reads" {
			out = append(out, name)
		}
	}
	return out
}

func TestOnlyLivePrepareNamesTheOrbitReadsEnvironment(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	if got := orbitReadsJobs(t, doc); !reflect.DeepEqual(got, []string{"live-prepare"}) {
		t.Errorf("the jobs whose environment is orbit-reads are %v, want exactly [live-prepare]: ORBIT_READ_TOKEN is scoped to that environment, so every job naming it (directly or through extends:) receives a token that reads ai/orbit (#303)", got)
	}
}

func TestOrbitReadsJobsSeesDirectScalarExtendedAndDefaultNaming(t *testing.T) {
	const base = `
.reads:
  environment:
    name: orbit-reads
live-prepare:
  environment:
    name: orbit-reads
    action: prepare
  script:
    - echo a
other:
  environment:
    name: live-pulls
  script:
    - echo b
bare:
  script:
    - echo d
`
	for _, tc := range []struct {
		name, extra string
		want        []string
	}{
		{"as written", "", []string{"live-prepare"}},
		{"a second job names it", "second:\n  environment:\n    name: orbit-reads\n  script:\n    - echo c\n", []string{"live-prepare", "second"}},
		{"a second job names it as a scalar", "second:\n  environment: orbit-reads\n  script:\n    - echo c\n", []string{"live-prepare", "second"}},
		{"default: names it", "default:\n  environment:\n    name: orbit-reads\n", []string{"live-prepare", "bare"}},
		{"a second job extends the template", "second:\n  extends: .reads\n  script:\n    - echo c\n", []string{"live-prepare", "second"}},
	} {
		doc, err := parseMiniYAML(base + tc.extra)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := orbitReadsJobs(t, doc); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: orbitReadsJobs = %v, want %v", tc.name, got, tc.want)
		}
	}
}
