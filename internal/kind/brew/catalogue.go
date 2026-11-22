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
	// byPlace are the casks by where they put their apps, lower case;
	// byQuit, those that say nowhere, by the bundle ids they quit.
	byPlace, byQuit map[string][]string
}

// catalogued is a cask as the catalogue has it.
type catalogued struct {
	token string
	// apps are where it puts its apps, from the Applications folder, as in
	// Example.app: each its app artifacts install, renamed by the target
	// given after it; for a cask installing through a package, with none,
	// each its uninstall deletes there.
	apps []string
	// ids are the bundle ids it states, lower case: those it quits,
	// signals or unloads (launchctl) as it's uninstalled or zapped, and
	// those the files in ~/Library it removes are named for.
	ids []string
	// quits are the bundle ids its uninstall quits, lower case, for a cask
	// installing through a package that says nowhere it puts an app.
	quits []string
	// version is its version, but its build after a comma, as in 5.2.3;
	// autoUpdates, whether its app updates itself, which Homebrew adopts
	// whatever its version.
	version     string
	autoUpdates bool
	// disabled is whether Homebrew has disabled it, or will on a date set.
	disabled bool
	// unvetted is whether it moves into place anything kit can't tell to
	// be its own: anything but an app in the Applications folder.
	unvetted bool
}

// vetted are the artifacts kit can tell a cask's own of: its apps in the
// Applications folder, by their bundle ids; and those Homebrew moves
// nothing into place for, as it links a binary or runs a package. --adopt
// takes over whatever is at a moved artifact's target, unchecked for a
// cask that updates itself, and --force deletes it, so a cask with any
// other (a suite, a plugin, a font, or a sort Homebrew adds later) is
// never adopted.
var vetted = []string{
	":app", ":uninstall", ":zap", ":pkg", ":installer", ":binary", ":command_wrapper", ":manpage",
	":bash_completion", ":zsh_completion", ":fish_completion", ":generate_completions_from_executable", ":generated_script",
	":preflight_steps", ":postflight_steps", ":uninstall_preflight_steps", ":uninstall_postflight_steps",
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
// artifacts, as written in its cask's source, its version, whether its app
// updates itself, and whether it's disabled; and the casks renamed.
type payload struct {
	Casks map[string]struct {
		Artifacts   []json.RawMessage `json:"raw_artifacts"`
		Version     string            `json:"version"`
		AutoUpdates bool              `json:"auto_updates"`
		Disable     json.RawMessage   `json:"disable_args"`
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
	c := &catalogue{casks: make(map[string]catalogued, len(p.Casks)), renames: p.Renames, byPlace: map[string][]string{}, byQuit: map[string][]string{}}
	for token, raw := range p.Casks {
		k := caskOf(raw.Artifacts)
		k.token, k.disabled = token, len(raw.Disable) > 0 && string(raw.Disable) != "null"
		k.version, _, _ = strings.Cut(raw.Version, ",")
		k.autoUpdates = raw.AutoUpdates
		c.casks[token] = k
		for _, at := range k.apps {
			place := strings.ToLower(at)
			c.byPlace[place] = append(c.byPlace[place], token)
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

// match is the cask that installs the app at at, from the Applications
// folder, whose bundle id is id and version have: of the casks putting an
// app there that state id, the one that's the app's (pick). An app
// anywhere else is no cask's to adopt: installing the cask would put a
// second copy in place. With none putting an app there, an app at the
// folder's top is the one package's whose uninstall quits id, though the
// package says nowhere it puts it: at worst, it installs a second copy
// under another name. Never one whose install would refuse the app
// (takesOver).
func (c *catalogue) match(at, id, have string) (string, bool) {
	candidates := c.byPlace[strings.ToLower(at)]
	if len(candidates) == 0 && filepath.Base(at) == at {
		candidates = c.byQuit[strings.ToLower(id)]
	}
	var stating []string
	for _, token := range candidates {
		if k := c.casks[token]; !k.disabled && !k.unvetted && k.states(id) {
			stating = append(stating, token)
		}
	}
	token, ok := c.pick(stating, have)
	if !ok || !c.casks[token].takesOver(have) {
		return "", false
	}
	return token, true
}

// takesOver reports whether installing the cask takes over its app of
// version have: Homebrew adopts it whatever its version when the app
// updates itself, else when it's the cask's version, and kit replaces it
// when it's an older copy of the same major version (replaces).
func (k catalogued) takesOver(have string) bool {
	return k.autoUpdates || sameVersion(have, k.version) || replaces(have, k.version)
}

// pick is the one of tokens that's the app's, of version have. The one
// without @ in its token, when it updates itself: Homebrew adopts the app
// whatever its version, deleting nothing, and an app that updates itself
// runs ahead of the catalogue, as Dropbox does of dropbox, at
// dropbox@beta's version. Else the one whose version shares the most of
// have (nearness), as blender@lts for Blender's LTS, a tie going to the one
// without @. None when more than one is without @.
func (c *catalogue) pick(tokens []string, have string) (string, bool) {
	plain := slices.DeleteFunc(slices.Clone(tokens), func(token string) bool { return strings.Contains(token, "@") })
	switch {
	case len(plain) > 1:
		return "", false
	case len(plain) == 1 && c.casks[plain[0]].autoUpdates:
		return plain[0], true
	}
	var nearest []string
	most := -1
	for _, token := range tokens {
		switch n := nearness(have, c.casks[token].version); {
		case n > most:
			nearest, most = []string{token}, n
		case n == most:
			nearest = append(nearest, token)
		}
	}
	switch {
	case len(nearest) == 1:
		return nearest[0], true
	case len(plain) == 1 && slices.Contains(nearest, plain[0]):
		return plain[0], true
	}
	return "", false
}

// states reports whether the cask states the bundle id id, in any case.
func (k catalogued) states(id string) bool {
	return id != "" && slices.Contains(k.ids, strings.ToLower(id))
}

// there reports whether the cask's apps are already in dir, the
// Applications folder; or why one there isn't the cask's: its bundle id
// isn't one the cask states, as a fork's under its name, or any app's for a
// cask that states none; it's the App Store's; or it can't be looked at.
func (k catalogued) there(dir, home string) (bool, error) {
	var found bool
	for _, at := range k.apps {
		path := filepath.Join(dir, at)
		_, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return false, fmt.Errorf("can't tell whose %s in %s is: %w", at, tilde.Shown(home, dir), err)
		case fromAppStore(path):
			return false, fmt.Errorf("%s is already in %s and is the App Store's", at, tilde.Shown(home, dir))
		}
		if id := apps.BundleID(path); !k.states(id) {
			return false, fmt.Errorf("%s is already in %s and isn't the cask's (%s)", at, tilde.Shown(home, dir), cmp.Or(id, "no bundle id"))
		}
		found = true
	}
	return found, nil
}

// replaceable is why the cask's apps already in dir aren't replaced by its
// own: nil when each is an older copy of the same major version
// (notReplacing).
func (k catalogued) replaceable(dir string) error {
	for _, at := range k.apps {
		path := filepath.Join(dir, at)
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		if err := notReplacing(at, apps.ShortVersion(path), k.version); err != nil {
			return err
		}
	}
	return nil
}

// caskOf is a cask as its raw artifacts have it: each a list of its sort,
// as in :app, then what it's given, a list of what it names, a map of its
// options, or both.
func caskOf(artifacts []json.RawMessage) catalogued {
	var k catalogued
	var app, pkg bool
	var deleted, quits []string
	for _, raw := range artifacts {
		var parts []json.RawMessage
		var sort string
		if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 || json.Unmarshal(parts[0], &sort) != nil {
			k.unvetted = true
			continue
		}
		var names []string
		options := map[string]json.RawMessage{}
		for _, part := range parts[1:] {
			_ = json.Unmarshal(part, &names)
			_ = json.Unmarshal(part, &options)
		}
		k.unvetted = k.unvetted || !slices.Contains(vetted, sort)
		switch sort {
		case ":app":
			var target string
			_ = json.Unmarshal(options[":target"], &target)
			at, ok := appsAt(names, target)
			k.apps, k.unvetted, app = append(k.apps, at...), k.unvetted || !ok, true
		case ":pkg":
			pkg = true
		case ":uninstall", ":zap":
			k.ids = append(k.ids, stated(options)...)
			if sort == ":uninstall" {
				deleted = append(deleted, oneOrMore(options[":delete"])...)
				quits = append(quits, oneOrMore(options[":quit"])...)
			}
		}
	}
	if pkg && !app {
		for _, path := range deleted {
			if at, ok := inApplications(path); ok && filepath.Ext(at) == ".app" && !strings.Contains(at, "*") {
				k.apps = append(k.apps, at)
			}
		}
		if len(k.apps) == 0 {
			k.quits = bundleIDs(quits)
		}
	}
	slices.Sort(k.ids)
	k.ids = slices.Compact(k.ids)
	return k
}

// appsAt are where an app artifact naming names puts them, from the
// Applications folder: each by its own name, the last where target says,
// if it's given; and whether every one goes there.
func appsAt(names []string, target string) (at []string, all bool) {
	all = true
	for i, name := range names {
		place := filepath.Base(name)
		if target != "" && i == len(names)-1 {
			place = target
		}
		rel, ok := inApplications(place)
		if ok {
			at = append(at, rel)
		}
		all = all && ok
	}
	return at, all
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
