package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

// ServiceStatesTimeout bounds the read-only `docker compose ps`: the
// answer only decorates Repair's first screen, so an unreachable daemon
// costs a few seconds, not a wait (#207: the limit lives beside the
// call, not in the UI). Repair's screen gives up on a reader at the
// same limit, so a reader that ignores it cannot keep "checking" up.
const ServiceStatesTimeout = 4 * time.Second

// The state and health words `docker compose ps` reports. The UI
// compares against these and shows them, so it types none of its own.
const (
	ContainerRunning    = "running"
	ContainerRestarting = "restarting"
	HealthUnhealthy     = "unhealthy"
	HealthStarting      = "starting"
)

// ServiceState is one container of the deployment as `docker compose
// ps -a --format json` reports it: the Compose service name, the
// container's state (running, exited, restarting, ...) and, where the
// service defines a health check, its health (healthy, unhealthy,
// starting; empty otherwise).
type ServiceState struct {
	Service, State, Health string
}

// Running reports whether the container is up and, where its service
// has a health check, neither failing it nor still starting. Every
// screen that counts or lists running services asks this, so none
// types the rule itself.
func (s ServiceState) Running() bool {
	return s.State == ContainerRunning && s.Health != HealthUnhealthy && s.Health != HealthStarting
}

// ServiceStates lists the deployment's containers, stopped ones
// included. It is read-only. The folder must be one nobody else could
// have written (RequireTrustedPath), as for every other Compose call.
func ServiceStates(ctx context.Context, dir string) ([]ServiceState, error) {
	if err := RequireTrustedPath(dir, EnvFile); err != nil {
		return nil, err
	}
	limit := limitApplied(ctx, ServiceStatesTimeout)
	ctx, cancel := context.WithTimeout(ctx, ServiceStatesTimeout)
	defer cancel()
	out, err := serviceStatesCommand(ctx, dir).Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("docker compose ps did not finish within %s: %w", limit, err)
		}
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	return parseServiceStates(out)
}

// serviceStatesCommand builds the exact command ServiceStates runs.
// --env-file is passed for the same reason StandDown passes it.
func serviceStatesCommand(ctx context.Context, dir string) *exec.Cmd {
	envFile := filepath.Join(dir, EnvFile)
	return dockerCommand(ctx, "compose",
		"--project-directory", dir, "--env-file", envFile,
		"ps", "-a", "--format", "json")
}

// parseServiceStates reads compose's output: one JSON object per line
// in current Compose, a single JSON array in older releases.
func parseServiceStates(out []byte) ([]ServiceState, error) {
	type record struct{ Service, State, Health string }
	dec := json.NewDecoder(bytes.NewReader(out))
	var states []ServiceState
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("reading docker compose ps output: %w", err)
		}
		var recs []record
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
			if err := json.Unmarshal(trimmed, &recs); err != nil {
				return nil, fmt.Errorf("reading docker compose ps output: %w", err)
			}
		} else {
			var r record
			if err := json.Unmarshal(trimmed, &r); err != nil {
				return nil, fmt.Errorf("reading docker compose ps output: %w", err)
			}
			recs = []record{r}
		}
		for _, r := range recs {
			states = append(states, ServiceState(r))
		}
	}
	return states, nil
}
