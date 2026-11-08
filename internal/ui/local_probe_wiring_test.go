package ui

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// #341: applyDeployment hands the probe seam the local health address
// built from .env-orbit, not APP_URL.

func localProbeApp(t *testing.T, env string, installed bool, probe func(context.Context, string) deploy.HealthReason) AppModel {
	t.Helper()
	return localProbeAppReading(t, env, installed, func(string) ([]deploy.ServiceState, error) { return nil, nil }, probe)
}

// localProbeAppReading is localProbeApp with the services reader given.
func localProbeAppReading(t *testing.T, env string, installed bool, read func(string) ([]deploy.ServiceState, error), probe func(context.Context, string) deploy.HealthReason) AppModel {
	t.Helper()
	t.Setenv("ORBIT_BIND_ADDRESS", "")
	t.Setenv("ORBIT_PORT", "")
	dir := t.TempDir()
	writeEnv(t, dir, env)
	if installed {
		testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile))
	}
	m := NewAppModelNoAnimation().WithoutRunningCheck()
	m.targetDir = dir
	m = m.WithServicesReader(read)
	return m.WithDeploymentStatus(probe)
}

func TestApplyDeployment_ProbesTheLocalAddressFromTheEnvFile(t *testing.T) {
	var urls []string
	m := localProbeApp(t,
		"APP_URL=https://mail.example.test\nORBIT_BIND_ADDRESS=198.51.100.9\nORBIT_PORT=8443\n", true,
		func(_ context.Context, u string) deploy.HealthReason {
			urls = append(urls, u)
			return deploy.HealthResponding
		})
	cmd := m.splash.probeHealthCmd()
	if cmd == nil {
		t.Fatal("no probe command for an installed deployment")
	}
	cmd()
	if len(urls) != 1 || urls[0] != "http://198.51.100.9:8443/api/health" {
		t.Errorf("probe asked %q, want exactly [http://198.51.100.9:8443/api/health]", urls)
	}
}

func TestApplyDeployment_WildcardAndDefaultsProbeLoopback(t *testing.T) {
	var got string
	m := localProbeApp(t, "APP_URL=https://mail.example.test\n", true,
		func(_ context.Context, u string) deploy.HealthReason { got = u; return deploy.HealthResponding })
	m.splash.probeHealthCmd()()
	if got != "http://127.0.0.1:3000/api/health" {
		t.Errorf("probe asked %q, want http://127.0.0.1:3000/api/health", got)
	}
}

func TestApplyDeployment_NonsenseBindAddressIsNotProbedAndStateStaysUnknown(t *testing.T) {
	probes := 0
	m := localProbeApp(t,
		"APP_URL=https://mail.example.test\nORBIT_BIND_ADDRESS=nonsense\n", true,
		func(context.Context, string) deploy.HealthReason { probes++; return deploy.HealthResponding })
	if cmd := m.splash.probeHealthCmd(); cmd != nil {
		cmd()
	}
	if probes != 0 {
		t.Errorf("probe seam called %d times for a nonsense bind address, want 0", probes)
	}
	if m.splash.state != stateUnknown {
		t.Errorf("state = %v, want stateUnknown", m.splash.state)
	}
}

// Not installed (only the pre-provisioned configuration): neither the
// splash's Init nor a return-to-menu runs a probe.
func TestApplyDeployment_NotInstalledRunsNoProbe(t *testing.T) {
	probes, reads := 0, 0
	m := localProbeAppReading(t,
		"APP_URL=https://mail.example.test\nORBIT_BIND_ADDRESS=198.51.100.9\nORBIT_PORT=8443\n", false,
		func(string) ([]deploy.ServiceState, error) { reads++; return nil, nil },
		func(context.Context, string) deploy.HealthReason { probes++; return deploy.HealthResponding })
	if cmd := m.splash.probeHealthCmd(); cmd != nil {
		cmd()
	}
	if cmd := m.splash.Init(); cmd != nil {
		drive(t, m.splash, cmd)
	}
	m, cmd := m.refreshSplash()
	if cmd != nil {
		drive(t, m, cmd)
	}
	if probes != 0 {
		t.Errorf("probe seam called %d times for a folder with no Compose file, want 0", probes)
	}
	if reads != 0 {
		t.Errorf("services reader called %d times for a folder with no Compose file, want 0", reads)
	}
	if m.splash.state != stateUnknown {
		t.Errorf("state = %v, want stateUnknown", m.splash.state)
	}
}

// #341: the splash gets the reader from WithServicesReader, and the
// reader is asked about the deployment folder, never the public URL.
func TestApplyDeployment_HandsTheSplashTheServicesReader(t *testing.T) {
	var asked []string
	var probed []string
	m := localProbeAppReading(t, "APP_URL=https://mail.example.test\n", true,
		func(dir string) ([]deploy.ServiceState, error) {
			asked = append(asked, dir)
			return []deploy.ServiceState{
				{Service: "orbit-db", State: "running"},
				{Service: "orbit-app", State: "running"},
				{Service: "orbit-clamav", State: "exited"},
			}, nil
		},
		func(_ context.Context, u string) deploy.HealthReason {
			probed = append(probed, u)
			return deploy.HealthResponding
		})
	msg, ok := m.splash.probeHealthCmd()().(healthResultMsg)
	if !ok {
		t.Fatal("not a healthResultMsg")
	}
	if len(asked) != 1 || asked[0] != m.targetDir {
		t.Errorf("reader asked about %q, want exactly [%s]", asked, m.targetDir)
	}
	if len(probed) != 1 || probed[0] != "http://127.0.0.1:3000/api/health" {
		t.Errorf("probe asked %q, want only the local URL", probed)
	}
	if !msg.asked || !msg.counted || msg.running != 2 || msg.expected != 3 ||
		!reflect.DeepEqual(msg.faults, []deploy.ServiceFault{{Service: "orbit-clamav", Word: deploy.FaultStopped}}) {
		t.Errorf("message = %+v, want 2 of 3 counted with orbit-clamav stopped", msg)
	}
}

// A nil reader: the HTTP answer decides alone and nothing is counted.
func TestApplyDeployment_NilReaderLeavesTheHTTPAnswerToDecide(t *testing.T) {
	m := localProbeAppReading(t, "APP_URL=https://mail.example.test\n", true, nil,
		func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding })
	msg, ok := m.splash.probeHealthCmd()().(healthResultMsg)
	if !ok {
		t.Fatal("not a healthResultMsg")
	}
	if !sameHealthMsg(msg, healthResultMsg{reason: deploy.HealthResponding}) {
		t.Errorf("message = %+v, want the HTTP answer alone, not counted", msg)
	}
}
