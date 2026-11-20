package readiness

import (
	"reflect"
	"strings"
	"testing"
)

const guideDoc = `# Releasing

1. A numbered line before the section is not a step.

## What you do, in short

Before step 1, on dev: rename Unreleased.

1. Open dev -> preview.
2. Manual test on preview.
3. Open preview -> main.
4. Press the button.

## Cutting a tag

1. Not a step either.
2. Nor this.
`

func TestGuideSteps_ListsOnlyTheNumberedLinesUnderTheSectionAndStopsAtTheNextHeading(t *testing.T) {
	steps, err := ParseGuideSteps(guideDoc)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 {
		t.Fatalf("got %d steps %q, want 4", len(steps), steps)
	}
	for i, want := range []string{"Open dev -> preview.", "Manual test on preview.", "Open preview -> main.", "Press the button."} {
		if !strings.Contains(steps[i], want) {
			t.Errorf("step %d = %q, want it to hold %q", i+1, steps[i], want)
		}
	}
}

func TestGuideSteps_NoSectionIsAnError(t *testing.T) {
	if _, err := ParseGuideSteps("# Releasing\n\n## Cutting a tag\n\n1. a\n"); err == nil {
		t.Error("a guide without the section must be an error")
	}
}

func TestGuideSteps_ASectionWithNoStepsIsAnError(t *testing.T) {
	if _, err := ParseGuideSteps("# Releasing\n\n## What you do, in short\n\nProse only.\n\n## Next\n\n1. a\n"); err == nil {
		t.Error("a section with no numbered steps must be an error")
	}
}

func rec(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestRecord_ParsesTheHeaderStepsAndSignOff(t *testing.T) {
	r := ParseRecord(rec(
		"version: v0.4.7",
		"guide-blob: 0123abcd",
		"guide-commit: ffff0000",
		"step 1: done: https://gitlab.example/ai/orbit-launcher/-/merge_requests/1",
		"step 2: not-needed: owner decided, no manual test",
		"step 3: after-merge",
		"Signed-off-by: Ada Tester",
	))
	if r.Version != "v0.4.7" && r.Version != "0.4.7" {
		t.Errorf("Version = %q", r.Version)
	}
	if r.GuideBlob != "0123abcd" {
		t.Errorf("GuideBlob = %q", r.GuideBlob)
	}
	if len(r.Steps) != 3 {
		t.Fatalf("Steps = %+v, want 3", r.Steps)
	}
	want := []Step{{1, "done", "https://gitlab.example/ai/orbit-launcher/-/merge_requests/1"}, {2, "not-needed", "owner decided, no manual test"}, {3, "after-merge", ""}}
	if !reflect.DeepEqual(r.Steps, want) {
		t.Errorf("Steps = %+v, want %+v", r.Steps, want)
	}
	if !reflect.DeepEqual(r.Signers, []string{"Ada Tester"}) {
		t.Errorf("Signers = %q", r.Signers)
	}
	if len(r.Problems) != 0 {
		t.Errorf("Problems = %v", r.Problems)
	}
}

func TestRecord_PlaceholderOrEmptyEvidenceIsAProblemAndNotCounted(t *testing.T) {
	for _, ev := range []string{"", "<link or job id>", "<...>", "todo", "TODO", "tbd", "TBC", "...", "-", "n/a", "N/A", "none", "xxx", "x"} {
		for _, status := range []string{"done", "not-needed"} {
			r := ParseRecord(rec("version: v0.4.7", "step 1: "+status+": "+ev))
			if len(r.Steps) != 0 {
				t.Errorf("step 1: %s: %q must not be counted; got %+v", status, ev, r.Steps)
			}
			if len(r.Problems) == 0 {
				t.Errorf("step 1: %s: %q must be a problem", status, ev)
			}
		}
	}
}

func TestRecord_AnUnknownStatusIsAProblemAndNotCounted(t *testing.T) {
	r := ParseRecord(rec("version: v0.4.7", "step 1: finished: https://x/1", "step 2: skipped"))
	if len(r.Steps) != 0 {
		t.Errorf("unknown statuses must not count; got %+v", r.Steps)
	}
	if len(r.Problems) < 2 {
		t.Errorf("each unknown status is a problem; got %v", r.Problems)
	}
}

func TestRecord_AfterMergeNeedsNoEvidence(t *testing.T) {
	r := ParseRecord(rec("version: v0.4.7", "step 1: after-merge"))
	if len(r.Steps) != 1 || r.Steps[0].Status != "after-merge" || len(r.Problems) != 0 {
		t.Errorf("Steps %+v Problems %v", r.Steps, r.Problems)
	}
}

func TestRecord_EverySignedOffByLineIsCollected(t *testing.T) {
	r := ParseRecord(rec("version: v0.4.7", "Signed-off-by: A Name", "Signed-off-by: Other Name"))
	if !reflect.DeepEqual(r.Signers, []string{"A Name", "Other Name"}) {
		t.Errorf("Signers = %q", r.Signers)
	}
}
