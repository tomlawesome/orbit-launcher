package deploy

import (
	"strings"
	"testing"
)

// 236-c11 (SP2-4): a newline inside a volume's label must not start a
// forged entry. Every listed volume's name still contains the Orbit
// volume pattern, because docker's own name filter is not applied to a
// line the label made up.
func TestUnownedDatabaseVolumes_ANewlineInALabelForgesNoEntries(t *testing.T) {
	// Real volume, whose project label holds a newline and more text; then
	// a forged "name<TAB>project" line and a forged line with no tab.
	fakeDockerPrinting(t, "real_orbit-db-data\tevil\nforged-name\tforged-project\njust some text\nlast-forged\t\n", 0)

	got := UnownedDatabaseVolumes(t.Context(), t.TempDir())
	if len(got) == 0 {
		t.Fatal("the real volume was dropped")
	}
	sawReal := false
	for _, v := range got {
		if !strings.Contains(v.Name, orbitDatabaseVolumePattern) {
			t.Errorf("listed %+v, whose name does not contain %q: a forged entry", v, orbitDatabaseVolumePattern)
		}
		if v.Name == "real_orbit-db-data" {
			sawReal = true
		}
	}
	if !sawReal {
		t.Errorf("the real volume is missing from %+v", got)
	}
}
