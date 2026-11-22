package brew

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/tilde"
)

// catalogue is Homebrew's own casks, as the copy of Homebrew's API in its
// cache has them, each by its token: brew info won't list every cask
// (Homebrew 7 refuses --eval-all), and this holds them all. Its format is
// Homebrew's own, internal one, versioned, so it can change: one that
// doesn't read is no catalogue, and matches nothing.
type catalogue struct {
	casks map[string]catalogued
	// renames are the tokens casks had before, each to the one it has now.
	renames map[string]string
	// byApp are the casks by the names of the apps they install, lower
	// case; byQuit, by the bundle ids their uninstalls quit.
	byApp, byQuit map[string][]string
}

// catalogued is a cask as the catalogue has it.
type catalogued struct {
	token string
	// apps are where it puts its apps, from the Applications folder, as in
	// Example.app: each its app artifacts install, renamed by the target
	// given after it, and, for a cask installing through a package, each
	// its uninstall deletes there. None it puts elsewhere, as in ~/Library.
	apps []string
	// ids are the bundle ids it states, lower case: those it quits,
	// signals or unloads (launchctl) as it's uninstalled or zapped, and
	// those the files in ~/Library it removes are named for. quits are
	// those its uninstall quits.
	ids, quits []string
	// disabled is whether Homebrew has disabled it, or will on a date set.
	disabled bool
}

// payloads are the catalogue's files in Homebrew's cache, one a platform,
// as in packages.arm64_tahoe.jws.json.payload: the newest is read.
var payloads = filepath.Join("api", "internal", "packages.*.jws.json.payload")

// catalogue is Homebrew's catalogue of casks, from the cache brew --cache
// names, asked once; read again only once its file has changed.
func (h *Homebrew) catalogue(ctx context.Context) (*catalogue, error) {
	h.cached.mu.Lock()
	defer h.cached.mu.Unlock()
	if h.cached.dir == "" {
		res, err := h.brew(ctx, "--cache")
		if err != nil {
			return nil, fmt.Errorf("find Homebrew's cache: %w", err)
		}
		if h.cached.dir = strings.TrimSpace(string(res.Stdout)); h.cached.dir == "" {
			return nil, errors.New("brew --cache named no folder")
		}
	}
	file, err := newest(filepath.Join(h.cached.dir, payloads))
	if err != nil {
		return nil, err
	}
	if h.cached.catalogue != nil && h.cached.file == file {
		return h.cached.catalogue, nil
	}
	c, err := readCatalogue(file.path)
	if err != nil {
		return nil, fmt.Errorf("read Homebrew's catalogue of casks, %s: %w", tilde.Shown(h.Home, file.path), err)
	}
	h.cached.catalogue, h.cached.file = c, file
	return c, nil
}

// stamp is a file as it was when looked at: where it is, when it was last
// modified, and its size.
type stamp struct {
	path      string
	mod, size int64
}

// newest is the newest of the files pattern matches.
func newest(pattern string) (stamp, error) {
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return stamp{}, err
	}
	var found stamp
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if s := (stamp{path: path, mod: info.ModTime().UnixNano(), size: info.Size()}); found.path == "" || s.mod > found.mod {
			found = s
		}
	}
	if found.path == "" {
		return stamp{}, errors.New("no catalogue of casks in Homebrew's cache")
	}
	return found, nil
}

// payload is the catalogue's file, as far as kit reads it: each cask's
// artifacts, as written in its cask's source, and whether it's disabled;
// and the casks renamed.
type payload struct {
	Casks map[string]struct {
		Artifacts []json.RawMessage `json:"raw_artifacts"`
		Disable   json.RawMessage   `json:"disable_args"`
	} `json:"casks"`
	Renames map[string]string `json:"cask_renames"`
}

// readCatalogue reads the catalogue's file at path: a line of its
// signature, then the catalogue itself.
func readCatalogue(path string) (*catalogue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	if _, err := r.ReadString('\n'); err != nil {
		return nil, fmt.Errorf("its signature: %w", err)
	}
	var p payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return nil, err
	}
	if p.Casks == nil {
		return nil, errors.New("it lists no casks")
	}
	c := &catalogue{casks: make(map[string]catalogued, len(p.Casks)), renames: p.Renames, byApp: map[string][]string{}, byQuit: map[string][]string{}}
	for token, raw := range p.Casks {
		k := caskOf(raw.Artifacts)
		k.token, k.disabled = token, len(raw.Disable) > 0 && string(raw.Disable) != "null"
		c.casks[token] = k
		for _, app := range k.apps {
			name := strings.ToLower(filepath.Base(app))
			c.byApp[name] = append(c.byApp[name], token)
		}
		for _, id := range k.quits {
			c.byQuit[id] = append(c.byQuit[id], token)
		}
	}
	return c, nil
}

// cask is the cask kit declares as name: by its token, or the token it
// had before, alone or in Homebrew's own tap, in any case. Another tap's
// isn't in the catalogue.
func (c *catalogue) cask(name string) (catalogued, bool) {
	token := strings.TrimPrefix(strings.ToLower(name), "homebrew/cask/")
	if k, ok := c.casks[token]; ok {
		return k, true
	}
	k, ok := c.casks[c.renames[token]]
	return k, ok
}

// match is the cask that installs the app named app, as in Example.app,
// whose bundle id is id: of the casks installing an app of that name, or,
// if none does, those whose uninstall quits id, the one that states id;
// one without @ in its token over those with (1password over
// 1password@beta). None when no cask states id, as for a fork of the
// cask's app, under its name, or when more than one would do. A disabled
// cask is never matched.
func (c *catalogue) match(app, id string) (string, bool) {
	id = strings.ToLower(id)
	candidates := c.byApp[strings.ToLower(app)]
	if len(candidates) == 0 {
		candidates = c.byQuit[id]
	}
	var stating []string
	for _, token := range candidates {
		if k := c.casks[token]; !k.disabled && k.states(id) {
			stating = append(stating, token)
		}
	}
	if plain := slices.DeleteFunc(slices.Clone(stating), func(token string) bool { return strings.Contains(token, "@") }); len(plain) > 0 {
		stating = plain
	}
	if len(stating) != 1 {
		return "", false
	}
	return stating[0], true
}

// states reports whether the cask states the bundle id id, in any case.
func (k catalogued) states(id string) bool {
	return id != "" && slices.Contains(k.ids, strings.ToLower(id))
}

// there are the cask's apps already in dir, the Applications folder, by
// path; or why one there isn't the cask's, as its bundle id isn't one the
// cask states: a fork of the cask's app, under its name, or any app at all
// when the cask states none. One that can't be looked at can't be told to
// be the cask's.
func (k catalogued) there(dir, home string) ([]string, error) {
	var found []string
	for _, at := range k.apps {
		path := filepath.Join(dir, at)
		_, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("can't tell whose %s in %s is: %w", at, tilde.Shown(home, dir), err)
		}
		if id := apps.BundleID(path); !k.states(id) {
			return nil, fmt.Errorf("%s is already in %s and isn't the cask's (%s)", at, tilde.Shown(home, dir), cmp.Or(id, "no bundle id"))
		}
		found = append(found, path)
	}
	return found, nil
}

// caskOf is a cask as its raw artifacts have it: each a list of its sort,
// as in :app, then what it's given, a list of what it names, a map of its
// options, or both.
func caskOf(artifacts []json.RawMessage) catalogued {
	var k catalogued
	var pkg bool
	var deleted []string
	for _, raw := range artifacts {
		var parts []json.RawMessage
		var sort string
		if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 || json.Unmarshal(parts[0], &sort) != nil {
			continue
		}
		var names []string
		options := map[string]json.RawMessage{}
		for _, part := range parts[1:] {
			_ = json.Unmarshal(part, &names)
			_ = json.Unmarshal(part, &options)
		}
		switch sort {
		case ":app":
			var target string
			_ = json.Unmarshal(options[":target"], &target)
			k.apps = append(k.apps, appsAt(names, target)...)
		case ":pkg":
			pkg = true
		case ":uninstall", ":zap":
			k.ids = append(k.ids, stated(options)...)
			if sort == ":uninstall" {
				k.quits = append(k.quits, bundleIDs(oneOrMore(options[":quit"]))...)
				deleted = append(deleted, oneOrMore(options[":delete"])...)
			}
		}
	}
	if pkg {
		for _, path := range deleted {
			if at, ok := inApplications(path); ok && filepath.Ext(at) == ".app" && !strings.Contains(at, "*") {
				k.apps = append(k.apps, at)
			}
		}
	}
	slices.Sort(k.ids)
	k.ids = slices.Compact(k.ids)
	return k
}

// appsAt are where an app artifact naming names puts them, from the
// Applications folder: each by its own name, the last where target says,
// if it's given.
func appsAt(names []string, target string) []string {
	var at []string
	for i, name := range names {
		place := filepath.Base(name)
		if target != "" && i == len(names)-1 {
			place = target
		}
		if rel, ok := inApplications(place); ok {
			at = append(at, rel)
		}
	}
	return at
}

// inApplications is path from the Applications folder: a relative one as
// it is, one in /Applications without it, and none for one elsewhere.
func inApplications(path string) (string, bool) {
	switch {
	case strings.HasPrefix(path, "~"):
		return "", false
	case filepath.IsAbs(path):
		rel, ok := strings.CutPrefix(path, "/Applications/")
		return rel, ok && rel != ""
	}
	return path, path != ""
}

// stated are the bundle ids an uninstall's or a zap's options state: those
// it quits, signals or unloads, and those the files in ~/Library it
// removes are named for.
func stated(options map[string]json.RawMessage) []string {
	ids := slices.Concat(oneOrMore(options[":quit"]), oneOrMore(options[":launchctl"]), signalled(options[":signal"]))
	for _, key := range []string{":trash", ":delete"} {
		for _, path := range oneOrMore(options[key]) {
			if m := libraryNamed.FindStringSubmatch(path); m != nil && !strings.Contains(path, "*") {
				ids = append(ids, m[1]+m[2]+m[3])
			}
		}
	}
	return bundleIDs(ids)
}

// libraryNamed finds the bundle id the files an app keeps in ~/Library are
// named for: its preferences, caches, container, HTTP storage, saved
// state, scripts and web data.
var libraryNamed = regexp.MustCompile(`^~/Library/(?:(?:Caches|Containers|HTTPStorages|Application Scripts|WebKit)/([^/]+)|Preferences/([^/]+)\.plist|Saved Application State/([^/]+)\.savedState)$`)

// signalled are the bundle ids an uninstall's signal names: one signal and
// id, or a list of them.
func signalled(raw json.RawMessage) []string {
	var one []string
	if json.Unmarshal(raw, &one) == nil && len(one) == 2 {
		return one[1:]
	}
	var more [][]string
	_ = json.Unmarshal(raw, &more)
	var ids []string
	for _, pair := range more {
		if len(pair) == 2 {
			ids = append(ids, pair[1])
		}
	}
	return ids
}

// bundleID is what a bundle id looks like, in lower case: reverse DNS, as
// in com.example.app.
var bundleID = regexp.MustCompile(`^[a-z0-9-]+(?:\.[a-z0-9_-]+)+$`)

// bundleIDs are those of names that look like bundle ids, lower case.
func bundleIDs(names []string) []string {
	var ids []string
	for _, name := range names {
		if id := strings.ToLower(name); bundleID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids
}
