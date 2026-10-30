// The Fedora leg of the live test (#183). `live` (Ubuntu) and `live-fedora`
// both extend the .live_leg template, so they run the same install, under the
// same rules and needs, and differ only in their image, LIVE_DISTRO and the
// package step that opens their before_script: apt on Ubuntu, dnf on Fedora.
// Everything after the package step -- the Go tarball, the in-job Docker
// daemon, the whole script -- is the same.
package supplychain

import "testing"

func TestLiveFedoraLegIsLiveOnAnotherImage(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	merged := map[string]*yamlNode{}
	for _, name := range []string{"live", "live-fedora"} {
		job, ok := doc.root.vals[name]
		if !ok || job.kind != 'm' {
			t.Fatalf("no %q job in .gitlab-ci.yml (#183)", name)
		}
		if ext, ok := job.vals["extends"]; !ok || ext.kind != 's' || ext.str != ".live_leg" {
			t.Errorf("%q must extend .live_leg, and only that (#183)", name)
		}
		m, err := doc.extendsMerge(job, 0)
		if err != nil {
			t.Fatal(err)
		}
		merged[name] = m
	}
	u, f := merged["live"], merged["live-fedora"]

	for _, k := range []string{"rules", "needs"} {
		if u.vals[k] == nil {
			t.Fatalf("`live` has no effective %s", k)
		}
	}
	keys := map[string]bool{}
	for _, m := range []*yamlNode{u, f} {
		for _, k := range m.keys {
			keys[k] = true
		}
	}
	for k := range keys {
		switch k {
		case "image", "variables", "before_script":
			continue // compared below
		}
		uv, fv := u.vals[k], f.vals[k]
		if uv == nil || fv == nil || uv.dump() != fv.dump() {
			t.Errorf("`live-fedora`'s effective %s differs from `live`'s: the legs must share everything but image, LIVE_DISTRO and the package step (#183)", k)
		}
	}

	if got := f.vals["image"].vals["name"].str; got != "$FEDORA_IMAGE" {
		t.Errorf("`live-fedora` runs %q, want $FEDORA_IMAGE (#183)", got)
	}
	if got := u.vals["image"].vals["name"].str; got != "$UBUNTU_IMAGE" {
		t.Errorf("`live` runs %q, want $UBUNTU_IMAGE (#183)", got)
	}
	for name, want := range map[string]string{"live": "ubuntu", "live-fedora": "fedora"} {
		v := merged[name].vals["variables"]
		if v == nil || v.vals["LIVE_DISTRO"] == nil || v.vals["LIVE_DISTRO"].str != want {
			t.Errorf("%q must set LIVE_DISTRO: %s (#183)", name, want)
			continue
		}
		if len(v.keys) != len(u.vals["variables"].keys) {
			t.Errorf("%q's variables %v: the legs may differ only in LIVE_DISTRO's value (#183)", name, v.keys)
		}
	}

	// The gate first, the Go tarball and the daemon last, on both legs.
	ub, fb := u.vals["before_script"].items, f.vals["before_script"].items
	if len(ub) < 3 || len(fb) < 3 {
		t.Fatalf("each leg's before_script must hold the gate, a package step, the Go tarball and the daemon")
	}
	if ub[0].dump() != fb[0].dump() {
		t.Error("both legs must open their before_script with the same skip gate (#164, #183)")
	}
	for i := 1; i <= 2; i++ {
		if ub[len(ub)-i].dump() != fb[len(fb)-i].dump() {
			t.Error("both legs must end their before_script with the same Go tarball and Docker daemon steps (#183)")
		}
	}
}
