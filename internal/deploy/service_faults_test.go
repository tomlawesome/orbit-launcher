package deploy

import (
	"reflect"
	"testing"
)

// #341: ServiceFaults names the expected services Running() denies, in
// expected order, so Repair and the splash share one rule.

var faultsExpected = []string{"orbit-db", "orbit-app", "orbit-clamav"}

func runningAll() []ServiceState {
	return []ServiceState{
		{Service: "orbit-db", State: "running", Health: "healthy"},
		{Service: "orbit-app", State: "running", Health: "healthy"},
		{Service: "orbit-clamav", State: "running", Health: "healthy"},
	}
}

func TestServiceFaults_AllRunningIsNil(t *testing.T) {
	if got := ServiceFaults(faultsExpected, runningAll()); len(got) != 0 {
		t.Errorf("faults = %+v, want none", got)
	}
	// A service with no health check reports an empty Health.
	have := append(runningAll(), ServiceState{Service: "orbit-tika", State: "running"})
	if got := ServiceFaults(append(append([]string{}, faultsExpected...), "orbit-tika"), have); len(got) != 0 {
		t.Errorf("faults = %+v, want none for a running service with empty health", got)
	}
}

func TestServiceFaults_Words(t *testing.T) {
	for name, c := range map[string]struct {
		state ServiceState
		want  string
	}{
		"unhealthy":  {ServiceState{Service: "orbit-clamav", State: "running", Health: "unhealthy"}, "unhealthy"},
		"starting":   {ServiceState{Service: "orbit-clamav", State: "running", Health: "starting"}, "starting"},
		"restarting": {ServiceState{Service: "orbit-clamav", State: "restarting"}, "restarting"},
		"exited":     {ServiceState{Service: "orbit-clamav", State: "exited"}, "stopped"},
		"created":    {ServiceState{Service: "orbit-clamav", State: "created"}, "stopped"},
		"paused":     {ServiceState{Service: "orbit-clamav", State: "paused"}, "stopped"},
		"dead":       {ServiceState{Service: "orbit-clamav", State: "dead"}, "stopped"},
	} {
		t.Run(name, func(t *testing.T) {
			have := []ServiceState{runningAll()[0], runningAll()[1], c.state}
			want := []ServiceFault{{Service: "orbit-clamav", Word: c.want}}
			if got := ServiceFaults(faultsExpected, have); !reflect.DeepEqual(got, want) {
				t.Errorf("faults = %+v, want %+v", got, want)
			}
		})
	}
}

func TestServiceFaults_AbsentServiceIsMissing(t *testing.T) {
	have := runningAll()[:2]
	want := []ServiceFault{{Service: "orbit-clamav", Word: "missing"}}
	if got := ServiceFaults(faultsExpected, have); !reflect.DeepEqual(got, want) {
		t.Errorf("faults = %+v, want %+v", got, want)
	}
	want = []ServiceFault{{"orbit-db", "missing"}, {"orbit-app", "missing"}, {"orbit-clamav", "missing"}}
	if got := ServiceFaults(faultsExpected, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("faults with nothing listed = %+v, want %+v", got, want)
	}
}

func TestServiceFaults_OrderFollowsExpectedNotDocker(t *testing.T) {
	have := []ServiceState{
		{Service: "orbit-clamav", State: "exited"},
		{Service: "orbit-app", State: "running", Health: "unhealthy"},
		{Service: "orbit-db", State: "running", Health: "healthy"},
	}
	want := []ServiceFault{{"orbit-app", "unhealthy"}, {"orbit-clamav", "stopped"}}
	if got := ServiceFaults(faultsExpected, have); !reflect.DeepEqual(got, want) {
		t.Errorf("faults = %+v, want %+v", got, want)
	}
}

func TestServiceFaults_UnexpectedServicesAreNeverFaults(t *testing.T) {
	have := append(runningAll(),
		ServiceState{Service: "orbit-tika", State: "exited"},
		ServiceState{Service: "orbit-ollama-model-pull", State: "exited"})
	if got := ServiceFaults(faultsExpected, have); len(got) != 0 {
		t.Errorf("faults = %+v, want none for services outside the expected set", got)
	}
}

func TestFaultWords_AreExact(t *testing.T) {
	if FaultMissing != "missing" {
		t.Errorf("FaultMissing = %q", FaultMissing)
	}
	if FaultStopped != "stopped" {
		t.Errorf("FaultStopped = %q", FaultStopped)
	}
}
