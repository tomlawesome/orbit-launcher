package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeHealth_HealthyOnAnyNonServerErrorStatus(t *testing.T) {
	for _, code := range []int{200, 302, 401, 404} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		if !ProbeHealth(context.Background(), srv.URL) {
			t.Errorf("status %d should read as alive (the app answered)", code)
		}
		srv.Close()
	}
}

func TestProbeHealth_DegradedOnServerErrorOrUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if ProbeHealth(context.Background(), srv.URL) {
		t.Error("502 should read as degraded")
	}
	if ProbeHealth(context.Background(), "http://127.0.0.1:1") {
		t.Error("connection refused should read as degraded")
	}
}

func TestProbeHealth_RespectsContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if ProbeHealth(ctx, srv.URL) {
		t.Error("timed-out probe should read as degraded")
	}
	if time.Since(start) > time.Second {
		t.Error("probe did not respect the context deadline")
	}
}
