package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 236-c8 (SP2-2): APP_URL comes from a settings file the launcher did not
// necessarily write, so only http and https addresses are ever probed.
// Anything else is "unchecked": no request is made and no status is read.
func TestProbeHealth_OnlyHTTPAndHTTPSAreProbed(t *testing.T) {
	for _, u := range []string{
		"file:///etc/hostname",
		"ftp://192.0.2.10/",
		"gopher://192.0.2.10/",
		"javascript:alert(1)",
		"data:text/plain,hello",
		"ssh://192.0.2.10/",
	} {
		if got := ProbeHealth(context.Background(), u); got != HealthUnchecked {
			t.Errorf("ProbeHealth(%q) = %q, want %q (not an http or https address)", u, got, HealthUnchecked)
		}
	}
}

// The scheme rule is not a blanket refusal: http and https still probe.
func TestProbeHealth_HTTPStillProbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthResponding {
		t.Errorf("an http address = %q, want %q", got, HealthResponding)
	}
}
