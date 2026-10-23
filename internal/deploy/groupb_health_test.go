package deploy

// Pins #277 W1 (ratified, answer 8a): the splash's health probe asks the
// address .env-orbit names with one GET, and
//
//   - never follows a redirect (a redirect would contact an address the
//     file never named; a 3xx answer still counts as responding);
//   - never sends credentials (a user:password@ in the address reaches
//     the server with no Authorization header);
//   - refuses a non-http(s) address as "could not check" and opens no
//     connection;
//   - gives up after 2 seconds even when the caller's context has no
//     deadline (or a longer one).

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupB_W1_RedirectIsNeverFollowed(t *testing.T) {
	for _, code := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
	} {
		var hits atomic.Int32
		second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, second.URL+"/elsewhere", code)
		}))
		got := ProbeHealth(context.Background(), first.URL)
		first.Close()
		second.Close()
		if got != HealthResponding {
			t.Errorf("redirect %d: ProbeHealth = %q, want responding (the redirect itself is an answer)", code, got)
		}
		if n := hits.Load(); n != 0 {
			t.Errorf("redirect %d: the redirect target saw %d request(s), want none", code, n)
		}
	}
}

func TestGroupB_W1_NoCredentialsAreSent(t *testing.T) {
	var mu sync.Mutex
	var auths []string
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="orbit"`)
		w.WriteHeader(http.StatusUnauthorized) // a 401 still counts as responding
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("user", "pw")
	got := ProbeHealth(context.Background(), u.String())

	if got != HealthResponding {
		t.Errorf("ProbeHealth with user:pw@ = %q, want responding (a 401 is an answer)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests == 0 {
		t.Fatal("the server saw no request: the address was not probed at all")
	}
	for i, a := range auths {
		if a != "" {
			t.Errorf("request %d carried Authorization %q, want none", i, a)
		}
	}
}

// A non-http(s) address is "could not check" and no connection is
// opened: a listener on the address's port sees nothing pending.
func TestGroupB_W1_NonHTTPAddressesOpenNoConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	tcp := ln.(*net.TCPListener)
	addr := ln.Addr().String()

	for _, u := range []string{
		"ftp://" + addr + "/",
		"gopher://" + addr + "/",
		"ssh://" + addr + "/",
		"ws://" + addr + "/",
		"wss://" + addr + "/",
		"tcp://" + addr,
		"//" + addr + "/",
		addr,
		"file:///etc/hostname",
		"javascript:alert(1)",
		"data:text/plain,hello",
		"",
		"HTTP-not://" + addr,
	} {
		if got := ProbeHealth(context.Background(), u); got != HealthUnchecked {
			t.Errorf("ProbeHealth(%q) = %q, want %q", u, got, HealthUnchecked)
		}
		// Anything already connected would be waiting to be accepted;
		// an immediate deadline turns "nothing waiting" into a timeout.
		_ = tcp.SetDeadline(time.Now())
		if c, err := ln.Accept(); err == nil {
			c.Close()
			t.Errorf("ProbeHealth(%q) opened a connection", u)
		} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			t.Fatalf("Accept: %v", err)
		}
	}
	if string(HealthUnchecked) != "could not check" {
		t.Errorf("HealthUnchecked reads %q, want the drawer's `could not check`", HealthUnchecked)
	}
}

// The 2 s limit holds when the caller's context has no deadline, and
// when it has a longer one.
func TestGroupB_W1_TwoSecondLimitHolds(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	long, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for name, ctx := range map[string]context.Context{"no deadline": context.Background(), "a minute": long} {
		start := time.Now()
		got := ProbeHealth(ctx, srv.URL)
		took := time.Since(start)
		if got != HealthNotResponding {
			t.Errorf("%s: a server that never answers = %q, want %q", name, got, HealthNotResponding)
		}
		if took > 3*time.Second {
			t.Errorf("%s: the probe took %v, want it to give up at about 2 s", name, took)
		}
		if took < time.Second {
			t.Errorf("%s: the probe gave up after %v, want the 2 s limit (not shorter)", name, took)
		}
	}
}
