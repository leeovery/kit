package boot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ClientID is kit's own app on GitHub, which a new Mac signs in through: a
// device flow app, public by design, with no secret.
const ClientID = "Ov23lidFySbOfyWe7ukf"

// scopes are what the sign-in asks for: what GitHub's CLI needs at least,
// as kit hands the sign-in to it; private repositories, as kit-config is
// one.
const scopes = "repo read:org"

// GitHub is GitHub, as the boot talks to it: its site, where a device signs
// in, and its API.
type GitHub struct {
	Client *http.Client
	// Site is https://github.com, and API https://api.github.com.
	Site, API string
	// ClientID is the app the sign-in goes through.
	ClientID string
	// Wait waits d, or till ctx is done: between asking whether the code's
	// been entered.
	Wait func(ctx context.Context, d time.Duration) error
}

// NewGitHub is GitHub itself, through client.
func NewGitHub(client *http.Client) GitHub {
	return GitHub{Client: client, Site: "https://github.com", API: "https://api.github.com", ClientID: ClientID, Wait: wait}
}

func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// deviceCode is a sign-in's code, as GitHub gives it.
type deviceCode struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	URI        string `json:"verification_uri"`
	ExpiresIn  int    `json:"expires_in"`
	Interval   int    `json:"interval"`
}

// errDenied is the sign-in declined on GitHub.
var errDenied = errors.New("the sign-in was declined on GitHub")

// errExpired is a code that expired before it was entered.
var errExpired = errors.New("the code expired")

// code asks GitHub for a code to sign in with.
func (g GitHub) code(ctx context.Context) (deviceCode, error) {
	var c deviceCode
	err := g.post(ctx, "/login/device/code", url.Values{"client_id": {g.ClientID}, "scope": {scopes}}, &c)
	if err != nil {
		return c, fmt.Errorf("ask GitHub for a code: %w", err)
	}
	if c.DeviceCode == "" || c.UserCode == "" {
		return c, errors.New("ask GitHub for a code: it gave none")
	}
	return c, nil
}

// token waits for code to be entered and approved, asking GitHub at the
// interval it set, and a little less often each time it says to slow down:
// the sign-in, once it's approved.
func (g GitHub) token(ctx context.Context, c deviceCode) (string, error) {
	interval := time.Duration(max(c.Interval, 1)) * time.Second
	for {
		if err := g.Wait(ctx, interval); err != nil {
			return "", err
		}
		var answer struct {
			Token string `json:"access_token"`
			Error string `json:"error"`
		}
		err := g.post(ctx, "/login/oauth/access_token", url.Values{
			"client_id": {g.ClientID}, "device_code": {c.DeviceCode},
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &answer)
		if err != nil {
			return "", fmt.Errorf("ask GitHub whether the code was entered: %w", err)
		}
		switch answer.Error {
		case "":
			if answer.Token == "" {
				return "", errors.New("GitHub approved the code but gave no sign-in")
			}
			return answer.Token, nil
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "expired_token":
			return "", errExpired
		case "access_denied":
			return "", errDenied
		default:
			return "", fmt.Errorf("GitHub said %s", answer.Error)
		}
	}
}

// post posts form to path on GitHub's site, and reads its JSON answer into
// v.
func (g GitHub) post(ctx context.Context, path string, form url.Values, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.Site+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return g.do(req, v)
}

// errSignedOut is a sign-in GitHub no longer takes.
var errSignedOut = errors.New("GitHub doesn't take the sign-in")

// get gets path from GitHub's API with token, and reads its JSON answer into
// v.
func (g GitHub) get(ctx context.Context, token, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.API+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return g.do(req, v)
}

func (g GitHub) do(req *http.Request, v any) error {
	res, err := g.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return errSignedOut
	case res.StatusCode == http.StatusNotFound:
		return errNotFound
	case res.StatusCode >= 300:
		return fmt.Errorf("GitHub answered %s", res.Status)
	}
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		return fmt.Errorf("read GitHub's answer: %w", err)
	}
	return nil
}

// errNotFound is a repository GitHub doesn't have, or won't show.
var errNotFound = errors.New("not found")

// login is who token signs in as.
func (g GitHub) login(ctx context.Context, token string) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := g.get(ctx, token, "/user", &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

// private reports whether the repository named repo, as owner/name, is
// private.
func (g GitHub) private(ctx context.Context, token, repo string) (bool, error) {
	var r struct {
		Private bool `json:"private"`
	}
	if err := g.get(ctx, token, "/repos/"+repo, &r); err != nil {
		return false, err
	}
	return r.Private, nil
}

// fetch fetches the repository named repo, its default branch as it stands,
// into dir, replacing what was there: a copy to read, not a clone.
func (g GitHub) fetch(ctx context.Context, token, repo, dir string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.API+"/repos/"+repo+"/tarball", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := g.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s", res.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), filepath.Base(dir)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := unpack(res.Body, tmp); err != nil {
		return fmt.Errorf("unpack %s: %w", repo, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

// unpack writes a repository's archive into dir: its files and folders,
// without the folder GitHub puts them in. Links are left out, as is
// anything that would land outside dir.
func unpack(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	archive := tar.NewReader(gz)
	for {
		h, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		_, name, found := strings.Cut(h.Name, "/")
		if !found || name == "" || !filepath.IsLocal(name) {
			continue
		}
		path := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := write(path, archive, os.FileMode(h.Mode)&0o700|0o600); err != nil {
				return err
			}
		}
	}
}

func write(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
