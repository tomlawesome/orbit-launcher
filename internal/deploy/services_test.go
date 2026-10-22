package deploy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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

func TestServiceStates_SlowDockerIsCutOffAndSaysSo(t *testing.T) {
	hangingDockerOnPath(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := ServiceStates(ctx, servicesDeployment(t))
	if err == nil || !strings.Contains(err.Error(), "did not finish within 1s") {
		t.Errorf("err = %v, want one saying it did not finish within 1s", err)
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
