package deploy

// #277: a Docker volume whose label holds a newline followed by an
// Orbit-looking volume name never adds a made-up entry to the
// stale-volume list. (236-c11's test covers made-up names that do not
// look like Orbit's; this one's do.)
//
// docker here is an emulation, not a printf: the listing it prints
// depends on the arguments the launcher passes, the way the real CLI's
// does (Go templates over the volume, --filter, inspect), so the test
// holds whichever query the launcher uses, not today's one.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"text/template"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

const composeProjectLabel = "com.docker.compose.project"

// emulatedVolume is one volume the emulated daemon holds.
type emulatedVolume struct {
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	CreatedAt string            `json:"created_at"`
}

// emulateDockerVolumes puts a docker on PATH that answers `volume ls`
// and `volume inspect` for vols as the real CLI would.
func emulateDockerVolumes(t *testing.T, vols ...emulatedVolume) {
	t.Helper()
	data, err := json.Marshal(vols)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "volumes.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	testutil.FakeExecutable(t, "docker", fmt.Sprintf(
		"#!/usr/bin/env bash\nexport ORBIT_EMULATED_DOCKER=%q\nexec %q -test.run='^TestHelperProcess_EmulatedDocker$' -- \"$@\"\n",
		file, bin))
}

// TestHelperProcess_EmulatedDocker is not a test: it is the body of the
// emulated docker (pattern: os/exec's helper process).
func TestHelperProcess_EmulatedDocker(t *testing.T) {
	file := os.Getenv("ORBIT_EMULATED_DOCKER")
	if file == "" {
		t.Skip("only runs as the emulated docker")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var vols []emulatedVolume
	if err := json.Unmarshal(raw, &vols); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sort.Slice(vols, func(i, j int) bool { return vols[i].Name < vols[j].Name })
	os.Exit(emulateDocker(args, vols))
}

func emulateDocker(args []string, vols []emulatedVolume) int {
	if len(args) < 2 || args[0] != "volume" {
		fmt.Fprintf(os.Stderr, "emulated docker: unsupported command %q\n", args)
		return 1
	}
	switch args[1] {
	case "ls", "list":
		return emulateVolumeLs(args[2:], vols)
	case "inspect":
		return emulateVolumeInspect(args[2:], vols)
	}
	fmt.Fprintf(os.Stderr, "emulated docker: unsupported command %q\n", args)
	return 1
}

// flagValue reads a flag's value from "--name value" or "--name=value".
func flagValue(args []string, i int, names ...string) (value string, next int, ok bool) {
	for _, n := range names {
		if args[i] == n && i+1 < len(args) {
			return args[i+1], i + 2, true
		}
		if strings.HasPrefix(args[i], n+"=") {
			return strings.TrimPrefix(args[i], n+"="), i + 1, true
		}
	}
	return "", i, false
}

// dockerFuncs are the docker CLI's template functions.
var dockerFuncs = template.FuncMap{
	"json": func(v any) (string, error) {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return "", err
		}
		return strings.TrimSpace(buf.String()), nil
	},
	"split": strings.Split,
	"join":  strings.Join,
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"title": strings.Title, //nolint:staticcheck // as the docker CLI does
	"pad": func(s string, l, r int) string {
		return strings.Repeat(" ", l) + s + strings.Repeat(" ", r)
	},
	"truncate": func(s string, n int) string {
		if len(s) > n {
			return s[:n]
		}
		return s
	},
	"println": fmt.Sprintln,
}

// lsVolume is the volume as `docker volume ls --format` sees it.
type lsVolume struct{ v emulatedVolume }

func (l lsVolume) Name() string         { return l.v.Name }
func (l lsVolume) Driver() string       { return "local" }
func (l lsVolume) Scope() string        { return "local" }
func (l lsVolume) Mountpoint() string   { return "/var/lib/docker/volumes/" + l.v.Name + "/_data" }
func (l lsVolume) Links() string        { return "N/A" }
func (l lsVolume) Size() string         { return "N/A" }
func (l lsVolume) Group() string        { return "N/A" }
func (l lsVolume) Availability() string { return "N/A" }
func (l lsVolume) Status() string       { return "N/A" }
func (l lsVolume) Label(name string) string {
	return l.v.Labels[name]
}
func (l lsVolume) Labels() string {
	var kv []string
	for k, v := range l.v.Labels {
		kv = append(kv, k+"="+v)
	}
	sort.Strings(kv)
	return strings.Join(kv, ",")
}
func (l lsVolume) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{
		"Availability": l.Availability(), "Driver": l.Driver(), "Group": l.Group(),
		"Labels": l.Labels(), "Links": l.Links(), "Mountpoint": l.Mountpoint(),
		"Name": l.Name(), "Scope": l.Scope(), "Size": l.Size(), "Status": l.Status(),
	})
}

func emulateVolumeLs(args []string, vols []emulatedVolume) int {
	var filters []string
	format, quiet := "", false
	for i := 0; i < len(args); {
		if v, next, ok := flagValue(args, i, "--filter", "-f"); ok {
			filters, i = append(filters, v), next
			continue
		}
		if v, next, ok := flagValue(args, i, "--format"); ok {
			format, i = v, next
			continue
		}
		if args[i] == "-q" || args[i] == "--quiet" {
			quiet, i = true, i+1
			continue
		}
		fmt.Fprintf(os.Stderr, "emulated docker: unsupported volume ls argument %q\n", args[i])
		return 1
	}
	var shown []emulatedVolume
	for _, v := range vols {
		if volumeMatches(v, filters) {
			shown = append(shown, v)
		}
	}
	switch {
	case quiet && format == "":
		format = "{{.Name}}"
	case format == "json":
		format = "{{json .}}"
	case format == "":
		fmt.Println("DRIVER    VOLUME NAME")
		format = "{{.Driver}}     {{.Name}}"
	}
	format = strings.TrimPrefix(format, "table ")
	tmpl, err := template.New("").Funcs(dockerFuncs).Parse(format)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, v := range shown {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, lsVolume{v}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(buf.String())
	}
	return 0
}

func volumeMatches(v emulatedVolume, filters []string) bool {
	for _, f := range filters {
		key, val, _ := strings.Cut(f, "=")
		switch key {
		case "name":
			if !strings.Contains(v.Name, val) {
				return false
			}
		case "label":
			lk, lv, hasValue := strings.Cut(val, "=")
			got, present := v.Labels[lk]
			if !present || (hasValue && got != lv) {
				return false
			}
		}
	}
	return true
}

// inspectVolume is the volume as `docker volume inspect` sees it.
type inspectVolume struct {
	CreatedAt  string
	Driver     string
	Labels     map[string]string
	Mountpoint string
	Name       string
	Options    map[string]string
	Scope      string
}

func emulateVolumeInspect(args []string, vols []emulatedVolume) int {
	format := ""
	var names []string
	for i := 0; i < len(args); {
		if v, next, ok := flagValue(args, i, "--format", "-f"); ok {
			format, i = v, next
			continue
		}
		names, i = append(names, args[i]), i+1
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, `"docker volume inspect" requires at least 1 argument.`)
		return 1
	}
	var found []inspectVolume
	status := 0
	for _, n := range names {
		ok := false
		for _, v := range vols {
			if v.Name == n {
				ok = true
				found = append(found, inspectVolume{
					CreatedAt: v.CreatedAt, Driver: "local", Labels: v.Labels,
					Mountpoint: "/var/lib/docker/volumes/" + v.Name + "/_data", Name: v.Name, Scope: "local",
				})
			}
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "Error response from daemon: get %s: no such volume\n", n)
			status = 1
		}
	}
	if format == "" {
		out, _ := json.MarshalIndent(found, "", "    ")
		if found == nil {
			out = []byte("[]")
		}
		fmt.Println(string(out))
		return status
	}
	if format == "json" {
		format = "{{json .}}"
	}
	tmpl, err := template.New("").Funcs(dockerFuncs).Parse(format)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, v := range found {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(buf.String())
	}
	return status
}

// volumeNames is the names in got, sorted.
func volumeNames(got []DatabaseVolume) []string {
	var names []string
	for _, v := range got {
		names = append(names, v.Name)
	}
	sort.Strings(names)
	return names
}

// The emulation lists ordinary volumes with their projects: guards the
// test below, whose point is an entry that must be absent.
func TestUnownedDatabaseVolumes_EmulatedDockerListsOrdinaryVolumes(t *testing.T) {
	emulateDockerVolumes(t,
		emulatedVolume{Name: "orbit_" + orbitDatabaseVolumePattern, Labels: map[string]string{composeProjectLabel: "orbit"}},
		emulatedVolume{Name: "old-tree_" + orbitDatabaseVolumePattern, Labels: map[string]string{composeProjectLabel: "old-tree"}},
		emulatedVolume{Name: "unrelated-volume", Labels: map[string]string{composeProjectLabel: "other"}},
	)

	got := UnownedDatabaseVolumes(t.Context(), t.TempDir())
	want := []string{"old-tree_" + orbitDatabaseVolumePattern, "orbit_" + orbitDatabaseVolumePattern}
	if names := volumeNames(got); strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("listed %+v, want exactly %q", got, want)
	}
	for _, v := range got {
		if wantProject := strings.TrimSuffix(v.Name, "_"+orbitDatabaseVolumePattern); v.Project != wantProject {
			t.Errorf("volume %s has project %q, want %q", v.Name, v.Project, wantProject)
		}
	}
}

func TestUnownedDatabaseVolumes_ANewlineInALabelForgesNoOrbitLookingEntry(t *testing.T) {
	forged := "madeup_" + orbitDatabaseVolumePattern
	lone := "lone_" + orbitDatabaseVolumePattern
	emulateDockerVolumes(t,
		// A real volume whose project label holds a newline, then a line
		// that reads like "name<TAB>project" for an Orbit-looking name,
		// then one that reads like a bare Orbit-looking name.
		emulatedVolume{Name: "real_" + orbitDatabaseVolumePattern, Labels: map[string]string{
			composeProjectLabel: "evil\n" + forged + "\tforged-project\n" + lone,
		}},
		emulatedVolume{Name: "orbit_" + orbitDatabaseVolumePattern, Labels: map[string]string{composeProjectLabel: "orbit"}},
	)

	got := UnownedDatabaseVolumes(t.Context(), t.TempDir())
	want := []string{"orbit_" + orbitDatabaseVolumePattern, "real_" + orbitDatabaseVolumePattern}
	if names := volumeNames(got); strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("listed %q, want exactly the two real volumes %q: a label's newline made up an entry", names, want)
	}
	for _, v := range got {
		if v.Name == "orbit_"+orbitDatabaseVolumePattern && v.Project != "orbit" {
			t.Errorf("the ordinary volume's project is %q, want orbit", v.Project)
		}
	}
}
