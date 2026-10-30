package live

import (
	"strings"
	"testing"
	"time"
)

const (
	oldDigest = "sha256:2a16d15bf2be55bcd40e80947f05f6e32e9c9d55cc5abf4021f72c71dc7bbed8"
	newDigest = "sha256:affc502ebf6820b7bd14f42a35f51c6f5827003bd1dfd81d8ae8c7ba9a647101"
	oldRef    = "registry.example.test/ai/orbit@" + oldDigest
	newRef    = "registry.example.test/ai/orbit@" + newDigest
)

// Two lines in the shape `docker events --format '{{json .}}'` prints, one
// a compose service and one a one-off, plus records the parser must skip.
const eventsSample = `{"Type":"container","Action":"create","Actor":{"ID":"aaa","Attributes":{"image":"` + newRef + `","name":"orbit-live-1-orbit-app-1","com.docker.compose.project":"orbit-live-1"}},"scope":"local","time":1791303931,"timeNano":1791303931000000001}

{"Type":"container","Action":"create","Actor":{"ID":"bbb","Attributes":{"image":"` + oldRef + `","name":"eager_turing"}},"scope":"local","time":1791303932,"timeNano":1791303932000000002}
{"Type":"container","Action":"start","Actor":{"ID":"bbb","Attributes":{"image":"` + oldRef + `","name":"eager_turing"}},"scope":"local","time":1791303932,"timeNano":1791303932000000003}
{"Type":"network","Action":"create","Actor":{"ID":"ccc","Attributes":{"name":"orbit-live-1_default"}},"scope":"local","time":1791303933,"timeNano":1791303933000000000}
`

func TestParseContainerCreates(t *testing.T) {
	got, err := parseContainerCreates(strings.NewReader(eventsSample))
	if err != nil {
		t.Fatalf("parseContainerCreates: %v", err)
	}
	want := []containerCreate{
		{At: time.Unix(0, 1791303931000000001), Image: newRef, Name: "orbit-live-1-orbit-app-1", Project: "orbit-live-1"},
		{At: time.Unix(0, 1791303932000000002), Image: oldRef, Name: "eager_turing"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d creates, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].At.Equal(want[i].At) || got[i].Image != want[i].Image || got[i].Name != want[i].Name || got[i].Project != want[i].Project {
			t.Errorf("create %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseContainerCreatesRefusesAGarbledLine(t *testing.T) {
	// A line that is not an event is a broken recording, not a quiet zero:
	// an empty parse would pass the "nothing from the old image" check.
	if _, err := parseContainerCreates(strings.NewReader("Error response from daemon: oops\n")); err == nil {
		t.Fatal("expected an error for a line that is not a JSON event")
	}
}

func TestDigestOf(t *testing.T) {
	for _, c := range []struct{ ref, want string }{
		{newRef, newDigest},
		{"ghcr.io/tomlawesome/orbit@" + oldDigest, oldDigest},
		{"postgres:17", ""},
		{"registry.example.test/ai/orbit@sha256:short", ""},
		{"", ""},
	} {
		if got := digestOf(c.ref); got != c.want {
			t.Errorf("digestOf(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestImageIdentityMatches(t *testing.T) {
	id := imageIdentity{Digest: newDigest, ID: "sha256:" + strings.Repeat("e", 64)}
	for _, c := range []struct {
		image string
		want  bool
	}{
		{newRef, true},
		{"ghcr.io/tomlawesome/orbit@" + newDigest, true}, // same image, other registry name
		{oldRef, false},
		{"sha256:" + strings.Repeat("e", 64), true}, // the daemon reporting an image ID
		{strings.Repeat("e", 64), true},
		{"postgres:17", false},
		{"", false},
	} {
		if got := id.matches(c.image); got != c.want {
			t.Errorf("matches(%q) = %v, want %v", c.image, got, c.want)
		}
	}
	if (imageIdentity{}).matches("") {
		t.Error("an empty identity must match nothing")
	}
}

func TestJudgeConfigureImage(t *testing.T) {
	base := time.Unix(1791303900, 0)
	from, to := base.Add(10*time.Second), base.Add(20*time.Second)
	old := imageIdentity{Digest: oldDigest}
	updated := imageIdentity{Digest: newDigest}
	oneOff := func(at time.Duration, ref string) containerCreate {
		return containerCreate{At: base.Add(at), Image: ref, Name: "one-off"}
	}
	service := func(at time.Duration, ref string) containerCreate {
		return containerCreate{At: base.Add(at), Image: ref, Name: "svc", Project: "orbit-live-1"}
	}

	for _, c := range []struct {
		name    string
		creates []containerCreate
		wantErr string
	}{
		{"the launcher's configure ran the new image", []containerCreate{
			oneOff(5*time.Second, newRef),  // install.sh's first attempt
			oneOff(12*time.Second, newRef), // --check in the tree
			oneOff(15*time.Second, newRef), // --set-oidc-secret in the tree
			service(30*time.Second, newRef),
			oneOff(13*time.Second, "postgres:17"),
		}, ""},
		{"configure ran the seeded old image", []containerCreate{
			oneOff(12*time.Second, oldRef),
			oneOff(15*time.Second, newRef),
		}, "old image"},
		{"the old image ran outside the configure window", []containerCreate{
			oneOff(5*time.Second, oldRef),
			oneOff(15*time.Second, newRef),
		}, "old image"},
		{"nothing ran while the launcher configured", []containerCreate{
			oneOff(5*time.Second, newRef),
			service(30*time.Second, newRef),
		}, "no configure one-off"},
		{"only a compose service in the window", []containerCreate{
			service(15*time.Second, newRef),
		}, "no configure one-off"},
	} {
		t.Run(c.name, func(t *testing.T) {
			summary, err := judgeConfigureImage(c.creates, from, to, old, updated)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if summary == "" {
					t.Error("expected a summary of the evidence for the log")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, c.wantErr)
			}
		})
	}

	t.Run("the same image on both sides proves nothing", func(t *testing.T) {
		if _, err := judgeConfigureImage([]containerCreate{oneOff(15*time.Second, newRef)}, from, to, updated, updated); err == nil {
			t.Fatal("expected an error when the old and new images are the same")
		}
	})
}

func TestEnvFileValue(t *testing.T) {
	data := []byte("# ORBIT_IMAGE=commented\nAPP_URL=https://x.example\nORBIT_IMAGE=" + oldRef + "\n ORBIT_IMAGE = \"" + newRef + "\" \n")
	if got := envFileValue(data, "ORBIT_IMAGE"); got != newRef {
		t.Errorf("ORBIT_IMAGE = %q, want the last assignment %q", got, newRef)
	}
	if got := envFileValue(data, "MISSING"); got != "" {
		t.Errorf("MISSING = %q, want empty", got)
	}
}

func TestManifestImageDigest(t *testing.T) {
	got, err := manifestImageDigest([]byte(`{"channel":"preview","image":{"repository":"registry.example.test/ai/orbit","digest":"` + newDigest + `"}}`))
	if err != nil || got != newDigest {
		t.Fatalf("manifestImageDigest = %q, %v; want %q", got, err, newDigest)
	}
	for _, bad := range []string{`{}`, `{"image":{"digest":"sha256:short"}}`, `not json`} {
		if _, err := manifestImageDigest([]byte(bad)); err == nil {
			t.Errorf("manifestImageDigest(%s): expected an error", bad)
		}
	}
}

func TestManifestLauncher_ReadsTheTagAndCommit(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	data := []byte(`{"channel":"preview","launcher":{"tag":"v0.4.2","commit":"` + commit + `"},"image":{"repository":"registry.example.test/ai/orbit","digest":"` + newDigest + `"}}`)
	tag, got, err := manifestLauncher(data)
	if err != nil || tag != "v0.4.2" || got != commit {
		t.Fatalf("manifestLauncher = %q, %q, %v; want v0.4.2, %q, nil", tag, got, err, commit)
	}
}

func TestManifestLauncher_RefusesAManifestWithNoLauncherTag(t *testing.T) {
	for _, bad := range []string{
		`{"image":{"digest":"` + newDigest + `"}}`,
		`{"launcher":{},"image":{"digest":"` + newDigest + `"}}`,
		`{"launcher":{"tag":"","commit":"0123456789abcdef0123456789abcdef01234567"}}`,
	} {
		_, _, err := manifestLauncher([]byte(bad))
		if err == nil {
			t.Errorf("manifestLauncher(%s): expected an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "launcher.tag") {
			t.Errorf("manifestLauncher(%s) error = %q, want it to name launcher.tag", bad, err)
		}
	}
	if _, _, err := manifestLauncher([]byte(`not json`)); err == nil {
		t.Errorf("manifestLauncher(not json): expected an error")
	}
}
