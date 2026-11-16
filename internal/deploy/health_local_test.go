package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// #341: the splash decides alive/degraded from the deployment's published
// port, not from the public address.

// clearBindEnv makes the process environment say nothing about the bind
// address and port (an empty value counts as unset).
func clearBindEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ORBIT_BIND_ADDRESS", "")
	t.Setenv("ORBIT_PORT", "")
}

func TestLocalHealthURL_Addresses(t *testing.T) {
	clearBindEnv(t)
	cases := []struct {
		name, addr, port, want string
	}{
		{"unset", "", "", "http://127.0.0.1:3000/api/health"},
		{"wildcard", "0.0.0.0", "", "http://127.0.0.1:3000/api/health"},
		{"v6 wildcard", "::", "", "http://[::1]:3000/api/health"},
		{"v6 wildcard bracketed", "[::]", "", "http://[::1]:3000/api/health"},
		{"lan address and port", "192.0.2.5", "8080", "http://192.0.2.5:8080/api/health"},
		{"loopback kept", "127.0.0.1", "", "http://127.0.0.1:3000/api/health"},
		{"v6 literal bracketed", "2001:db8::5", "", "http://[2001:db8::5]:3000/api/health"},
		{"v6 loopback", "::1", "9000", "http://[::1]:9000/api/health"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := LocalHealthURL(&Deployment{BindAddress: c.addr, Port: c.port})
			if err != nil || got != c.want {
				t.Errorf("LocalHealthURL = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestLocalHealthURL_EnvironmentWinsOverFile(t *testing.T) {
	t.Setenv("ORBIT_BIND_ADDRESS", "198.51.100.7")
	t.Setenv("ORBIT_PORT", "9090")
	got, err := LocalHealthURL(&Deployment{BindAddress: "192.0.2.5", Port: "8080"})
	if err != nil || got != "http://198.51.100.7:9090/api/health" {
		t.Errorf("LocalHealthURL = %q, %v; want the environment's values", got, err)
	}
}

func TestLocalHealthURL_EmptyEnvironmentFallsThroughToFile(t *testing.T) {
	clearBindEnv(t)
	got, err := LocalHealthURL(&Deployment{BindAddress: "192.0.2.5", Port: "8080"})
	if err != nil || got != "http://192.0.2.5:8080/api/health" {
		t.Errorf("LocalHealthURL = %q, %v; want the file's values", got, err)
	}
}

func TestLocalHealthURL_EnvironmentOverridesOneKeyOnly(t *testing.T) {
	clearBindEnv(t)
	t.Setenv("ORBIT_PORT", "9090")
	got, err := LocalHealthURL(&Deployment{BindAddress: "192.0.2.5", Port: "8080"})
	if err != nil || got != "http://192.0.2.5:9090/api/health" {
		t.Errorf("LocalHealthURL = %q, %v; want file address, environment port", got, err)
	}
}

func TestLocalHealthURL_RejectsWhatComposeWouldNotPublish(t *testing.T) {
	clearBindEnv(t)
	for _, c := range []struct{ name, addr, port string }{
		{"hostname localhost", "localhost", ""},
		{"dns name", "orbit.example.com", ""},
		{"address with port", "0.0.0.0:3000", ""},
		{"port zero", "", "0"},
		{"port too large", "", "65536"},
		{"port not a number", "", "abc"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := LocalHealthURL(&Deployment{BindAddress: c.addr, Port: c.port})
			if err == nil {
				t.Errorf("LocalHealthURL = %q, nil; want an error", got)
			}
		})
	}
}

func TestDetect_ReadsBindAddressAndPort(t *testing.T) {
	dir := t.TempDir()
	env := "APP_URL=https://mail.example.test\nORBIT_BIND_ADDRESS=198.51.100.9\nORBIT_PORT=8443\n"
	if err := os.WriteFile(filepath.Join(dir, EnvFile), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(dir)
	if err != nil || d == nil {
		t.Fatalf("Detect = %v, %v", d, err)
	}
	if d.BindAddress != "198.51.100.9" || d.Port != "8443" {
		t.Errorf("BindAddress, Port = %q, %q; want 198.51.100.9, 8443", d.BindAddress, d.Port)
	}
}

func localServer(t *testing.T, h http.HandlerFunc) (string, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/api/health", &n
}

func TestProbeHealth_DecidesFromTheHealthAnswerAlone(t *testing.T) {
	cases := []struct {
		name  string
		code  int
		ctype string
		body  string
		want  HealthReason
	}{
		{"ready", 200, "application/json", `{"status":"ready"}`, HealthResponding},
		{"maintenance", 200, "application/json", `{"status":"maintenance"}`, HealthMaintenance},
		{"503 degraded", 503, "application/json", `{"status":"degraded"}`, HealthServerError},
		{"200 degraded", 200, "application/json", `{"status":"degraded"}`, HealthServerError},
		{"html page", 200, "text/html", `<html>hello</html>`, HealthUnexpectedAnswer},
		{"404", 404, "text/plain", `not found`, HealthUnexpectedAnswer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, n := localServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", c.ctype)
				w.WriteHeader(c.code)
				_, _ = w.Write([]byte(c.body))
			})
			if got := ProbeHealth(context.Background(), url); got != c.want {
				t.Errorf("= %q, want %q", got, c.want)
			}
			if n.Load() != 1 {
				t.Errorf("%d requests, want exactly 1 (no page fallback)", n.Load())
			}
		})
	}
}

func TestHealthUnexpectedAnswer_Text(t *testing.T) {
	if string(HealthUnexpectedAnswer) != "unexpected answer" {
		t.Errorf("HealthUnexpectedAnswer = %q, want %q", HealthUnexpectedAnswer, "unexpected answer")
	}
}

func TestProbeHealth_RedirectIsUnexpectedAndNeverFollowed(t *testing.T) {
	var target atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target.Add(1)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}))
	defer other.Close()
	url, n := localServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/health", http.StatusFound)
	})
	if got := ProbeHealth(context.Background(), url); got != HealthUnexpectedAnswer {
		t.Errorf("302 = %q, want %q", got, HealthUnexpectedAnswer)
	}
	if target.Load() != 0 {
		t.Error("the redirect target was requested")
	}
	if n.Load() != 1 {
		t.Errorf("%d requests, want 1", n.Load())
	}
}

func TestProbeHealth_RefusedIsNotResponding(t *testing.T) {
	if got := ProbeHealth(context.Background(), "http://127.0.0.1:1/api/health"); got != HealthNotResponding {
		t.Errorf("refused = %q, want %q", got, HealthNotResponding)
	}
}

func TestProbeHealth_HandlerPastTwoSecondsIsNotResponding(t *testing.T) {
	url, _ := localServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	start := time.Now()
	if got := ProbeHealth(context.Background(), url); got != HealthNotResponding {
		t.Errorf("hung handler = %q, want %q", got, HealthNotResponding)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("probe took %v, want about 2s", d)
	}
}

func TestProbeHealth_SendsNoCredentials(t *testing.T) {
	var auth atomic.Value
	url, _ := localServer(t, func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	withUser := "http://user:pw@" + url[len("http://"):]
	if got := ProbeHealth(context.Background(), withUser); got != HealthResponding {
		t.Fatalf("= %q, want responding", got)
	}
	if v, _ := auth.Load().(string); v != "" {
		t.Errorf("Authorization header %q sent, want none", v)
	}
}
