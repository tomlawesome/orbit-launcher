package deploy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

func TestParseServiceStates(t *testing.T) {
	want := []ServiceState{
		{Service: "app", State: "running", Health: "healthy"},
		{Service: "db", State: "exited"},
	}
	t.Run("one object per line", func(t *testing.T) {
		out := `{"Service":"app","State":"running","Health":"healthy"}` + "\n" +
			`{"Service":"db","State":"exited","Health":""}` + "\n"
		got, err := parseServiceStates([]byte(out))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("a single array", func(t *testing.T) {
		out := `[{"Service":"app","State":"running","Health":"healthy"},{"Service":"db","State":"exited"}]`
		got, err := parseServiceStates([]byte(out))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("empty output", func(t *testing.T) {
		for _, out := range []string{"", "\n  \n"} {
			got, err := parseServiceStates([]byte(out))
			if err != nil || len(got) != 0 {
				t.Errorf("output %q: got %+v, %v; want no rows and no error", out, got, err)
			}
		}
	})
	t.Run("malformed input", func(t *testing.T) {
		for _, out := range []string{`not json`, `{"Service":`, `[{"Service":1}]`, `{"Service":["x"]}`} {
			if got, err := parseServiceStates([]byte(out)); err == nil {
				t.Errorf("output %q: got %+v, want an error", out, got)
			}
		}
	})
}

func TestServiceStatesCommand_IsTheExactReadOnlyCall(t *testing.T) {
	dir := "/opt/orbit"
	cmd := serviceStatesCommand(context.Background(), dir)
	want := []string{"compose", "--project-directory", dir, "--env-file", filepath.Join(dir, EnvFile), "ps", "-a", "--format", "json"}
	if !reflect.DeepEqual(cmd.Args[1:], want) {
		t.Errorf("args = %q, want %q", cmd.Args[1:], want)
	}
}

func servicesDeployment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
	return dir
}

func TestServiceStates_UntrustedFolderIsRefusedWithoutRunningDocker(t *testing.T) {
	callLog := fakeDockerLogging(t)
	if got, err := ServiceStates(t.Context(), t.TempDir()); err == nil {
		t.Errorf("got %+v, nil; want an error for a folder without %s", got, EnvFile)
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Errorf("docker was run for an untrusted folder (stat err = %v)", err)
	}
}

func TestServiceStates_ReturnsWhatComposeReports(t *testing.T) {
	fakeDockerPrinting(t, `{"Service":"app","State":"running","Health":"healthy"}`+"\n"+`{"Service":"db","State":"restarting"}`+"\n", 0)
	got, err := ServiceStates(t.Context(), servicesDeployment(t))
	want := []ServiceState{{"app", "running", "healthy"}, {"db", "restarting", ""}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, %v; want %+v", got, err, want)
	}
}

func TestServiceStates_FailedDockerIsAnError(t *testing.T) {
	fakeDockerPrinting(t, "Cannot connect to the Docker daemon\n", 1)
	_, err := ServiceStates(t.Context(), servicesDeployment(t))
	if err == nil || !strings.Contains(err.Error(), "docker compose ps") {
		t.Errorf("err = %v, want one naming docker compose ps", err)
	}
}

func TestServiceStates_UnreadableOutputIsAnError(t *testing.T) {
	fakeDockerPrinting(t, "not json\n", 0)
	if _, err := ServiceStates(t.Context(), servicesDeployment(t)); err == nil {
		t.Error("want an error for output that is not JSON")
	}
}

// slowDockerLimit is the caller's deadline in the test below.
const slowDockerLimit = time.Second

// The error names the caller's deadline, not the launcher's own longer
// limit (#207). What it names is what was left of the deadline when the
// call began, which is a little under slowDockerLimit by however long the
// trusted-path check took (#283), so the test reads the duration out of
// the text instead of expecting "1s". The folder is made before the
// deadline starts so set-up time does not eat into it.
func TestServiceStates_SlowDockerIsCutOffAndSaysSo(t *testing.T) {
	hangingDockerOnPath(t)
	dir := servicesDeployment(t)
	ctx, cancel := context.WithTimeout(t.Context(), slowDockerLimit)
	defer cancel()
	_, err := ServiceStates(ctx, dir)
	if err == nil {
		t.Fatal("err = nil, want one saying docker did not finish in time")
	}
	m := regexp.MustCompile(`did not finish within (\S+?):`).FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("err = %v, want one saying how long it did not finish within", err)
	}
	named, perr := time.ParseDuration(m[1])
	if perr != nil {
		t.Fatalf("err = %v: %q is not a duration: %v", err, m[1], perr)
	}
	if named <= 0 || named > slowDockerLimit {
		t.Errorf("err = %v: names %v, want the caller's deadline (above 0, at most %v), not the launcher's own limit %v",
			err, named, slowDockerLimit, ServiceStatesTimeout)
	}
}

func TestServiceState_Running(t *testing.T) {
	for _, tc := range []struct {
		state, health string
		want          bool
	}{
		{"running", "healthy", true},
		{"running", "", true},
		{"running", "unhealthy", false},
		{"running", "starting", false},
		{"exited", "", false},
		{"restarting", "", false},
		{"", "", false},
	} {
		s := ServiceState{Service: "orbit-app", State: tc.state, Health: tc.health}
		if got := s.Running(); got != tc.want {
			t.Errorf("Running() for state %q health %q = %v, want %v", tc.state, tc.health, got, tc.want)
		}
	}
}
