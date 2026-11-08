package ui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// #341: applyDeployment hands the probe seam the local health address
// built from .env-orbit, not APP_URL.

func localProbeApp(t *testing.T, env string, installed bool, probe func(context.Context, string) deploy.HealthReason) AppModel {
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
	m = m.WithServicesReader(func(string) ([]deploy.ServiceState, error) { return nil, nil })
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
	probes := 0
	m := localProbeApp(t,
		"APP_URL=https://mail.example.test\nORBIT_BIND_ADDRESS=198.51.100.9\nORBIT_PORT=8443\n", false,
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
	if m.splash.state != stateUnknown {
		t.Errorf("state = %v, want stateUnknown", m.splash.state)
	}
}
