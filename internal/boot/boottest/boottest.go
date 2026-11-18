// Package boottest is GitHub as tests have it, for what signs in and reads
// repositories as the boot does: someone, signed in with Token, and the
// repositories they can see.
package boottest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/boot"
)

// Token is the sign-in GitHub takes, plainly no real one, and Login who it
// signs in as.
const (
	Token = "gho_token"
	Login = "someone"
)

// Code is the code GitHub gives a device to sign in with, to enter at its
// site's page for it.
const Code = "WDJB-MJHT"

// Repos are the repositories GitHub has, by owner/name, each private or
// not: added to while it serves them, as gh repo create adds one. A device
// signing in waits till its code's Entered.
type Repos struct {
	mu      sync.Mutex
	have    map[string]bool
	entered chan struct{}
	enter   sync.Once
}

// Entered has the code a device signs in with entered and approved.
func (r *Repos) Entered() { r.enter.Do(func() { close(r.entered) }) }

// Add has GitHub hold repo, private or not.
func (r *Repos) Add(repo string, private bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.have[repo] = private
}

func (r *Repos) private(repo string) (private, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	private, ok = r.have[repo]
	return private, ok
}

func (r *Repos) privateOnes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for name, private := range r.have {
		if private {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, strings.Compare)
	return names
}

// Serve is GitHub, on this machine, for the test's length: Login, signed in
// with Token, seeing repos, by owner/name, each private or not; listing the
// private ones alone; a device signing in, with Code, once it's Entered.
func Serve(t *testing.T, repos map[string]bool) boot.GitHub {
	gh, _ := ServeRepos(t, repos)
	return gh
}

// ServeRepos is GitHub as Serve has it, and the repositories it holds, to
// add to.
func ServeRepos(t *testing.T, repos map[string]bool) (boot.GitHub, *Repos) {
	t.Helper()
	have := &Repos{have: map[string]bool{}, entered: make(chan struct{})}
	for name, private := range repos {
		have.Add(name, private)
	}
	mux := http.NewServeMux()
	signedIn := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+Token }
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if !signedIn(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"login": Login})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}", func(w http.ResponseWriter, r *http.Request) {
		private, ok := have.private(r.PathValue("owner") + "/" + r.PathValue("name"))
		if !ok || !signedIn(r) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"private": private})
	})
	mux.HandleFunc("GET /user/repos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("visibility") != "private" {
			t.Errorf("listed repositories with %s, want the private ones alone", r.URL.RawQuery)
		}
		list := []map[string]string{}
		for _, name := range have.privateOnes() {
			list = append(list, map[string]string{"full_name": name})
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("POST /login/device/code", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "device", "user_code": Code, "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5})
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-have.entered:
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": Token})
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return boot.GitHub{Client: srv.Client(), Site: srv.URL, API: srv.URL, ClientID: boot.ClientID, Wait: func(context.Context, time.Duration) error { return nil }}, have
}
