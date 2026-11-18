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

// Serve is GitHub, on this machine, for the test's length: Login, signed in
// with Token, seeing repos, by owner/name, each private or not; listing the
// private ones alone.
func Serve(t *testing.T, repos map[string]bool) boot.GitHub {
	t.Helper()
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
		private, ok := repos[r.PathValue("owner")+"/"+r.PathValue("name")]
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
		var list []map[string]string
		for name, private := range repos {
			if private {
				list = append(list, map[string]string{"full_name": name})
			}
		}
		slices.SortFunc(list, func(a, b map[string]string) int { return strings.Compare(a["full_name"], b["full_name"]) })
		_ = json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return boot.GitHub{Client: srv.Client(), Site: srv.URL, API: srv.URL, ClientID: boot.ClientID, Wait: func(context.Context, time.Duration) error { return nil }}
}
