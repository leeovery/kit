// Package defaults is macOS settings as a kind, default: each declared in a
// [macos settings] section in defaults write's form, compared with what
// defaults export says.
//
// A setting the Mac has never had as declared (a new Mac, a newly declared
// line) is missing, and applying writes it. One kit has seen as declared,
// and that differs now, was changed on the Mac, in System Settings: it's
// diverged, and applying leaves it for kit reconcile to adopt or revert.
// kit also watches a set of Apple's domains, and every domain a setting is
// declared in, so a setting changed there and not declared shows up, to
// adopt or put back.
package defaults

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// recordName names kit's record of the settings it has seen as declared,
// and the watched settings' values as it last accepted them.
const recordName = "settings.json"

type record struct {
	// Seen are the declared settings kit has seen as declared, by name.
	Seen map[string]bool `json:"seen,omitempty"`
	// Domains are the watched domains whose values kit has taken, by
	// export, as in currentHost:NSGlobalDomain.
	Domains map[string]bool `json:"domains,omitempty"`
	// Baseline is each watched setting's value as kit last accepted it, as
	// XML, by name.
	Baseline map[string]string `json:"baseline,omitempty"`
}

// Watched are Apple's domains whose settings kit watches for changes, as
// well as every domain a setting is declared in: the Dock, Finder, the
// global domain, the trackpad, the keyboard, screenshots, the menu bar
// clock, Control Center, Stage Manager, Spaces and keyboard shortcuts.
var Watched = []string{
	"NSGlobalDomain",
	"com.apple.dock",
	"com.apple.finder",
	"com.apple.AppleMultitouchTrackpad",
	"com.apple.driver.AppleBluetoothMultitouch.trackpad",
	"com.apple.HIToolbox",
	"com.apple.screencapture",
	"com.apple.menuextra.clock",
	"com.apple.controlcenter",
	"com.apple.WindowManager",
	"com.apple.spaces",
	"com.apple.symbolichotkeys",
	"com.apple.desktopservices",
}

// noise are the keys macOS changes by itself, by domain ("*" for any), as
// path.Match patterns: never shown as changed.
var noise = map[string][]string{
	"*": {
		"NSWindow Frame *", "NSSplitView Subview Frames *", "NSTableView *", "NSToolbar Configuration *",
		"NSNavPanel*", "NSNavLastRootDirectory", "NSNavRecentPlaces", "*RecentDocuments*", "*LastUsed*",
		"*Timestamp*", "*timestamp*", "*-stamp", "*Date", "*date",
	},
	"NSGlobalDomain": {"com.apple.gms.*", "NSLinguisticDataAssets*", "AKLastIDMSEnvironment", "NSPreferredWebServices", "com.apple.springing.*"},
	"com.apple.dock": {"persistent-apps", "persistent-others", "recent-apps", "mod-count", "trash-full", "lastShowIndicatorTime", "region", "loc", "workspaces-*"},
	"com.apple.finder": {
		"FXRecentFolders", "GoToField*", "FXDesktopVolumePositions", "FXConnectToBounds", "FXConnectToLastURL",
		"TrashViewSettings", "SearchRecentsSavedViewStyle*", "ComputerViewSettings", "FK_*", "LastTrashState", "FXSidebarUpgradedTo*",
	},
}

// Defaults is macOS settings, driven through defaults.
type Defaults struct {
	run      runner.Runner
	stateDir string
	// declared are the declared values, by name.
	declared map[string]declaredValue
	admin    func(ctx context.Context) bool
}

// declaredValue is a setting as declared: where it is, and its value.
type declaredValue struct {
	setting config.Setting
	value   any
	// words are the value as defaults write takes it, after the key.
	words []string
}

// New returns macOS settings, driven through run, with kit's records in
// stateDir.
func New(run runner.Runner, stateDir string) *Defaults {
	return &Defaults{run: run, stateDir: stateDir, declared: map[string]declaredValue{}}
}

func (d *Defaults) Name() string    { return "default" }
func (d *Defaults) Title() string   { return "macOS settings" }
func (d *Defaults) Program() string { return "defaults" }

// Diverges marks a setting changed from what kit saw declared as changed on
// purpose.
func (d *Defaults) Diverges() {}

// Values reads each setting's declared value.
func (d *Defaults) Values(list config.List) (config.List, error) {
	d.declared = make(map[string]declaredValue, len(list.Entries))
	for _, e := range list.Entries {
		v, err := readDeclared(e)
		if err != nil {
			return list, fmt.Errorf("%s: %s: %w", e.Pos(), e.Name, err)
		}
		d.declared[e.Name] = v
	}
	return list, nil
}

// readDeclared reads an entry's setting and value.
func readDeclared(e config.Entry) (declaredValue, error) {
	s, err := config.ParseSetting(e.Name)
	if err != nil {
		return declaredValue{}, err
	}
	words, err := config.Words(e.Value)
	if err != nil {
		return declaredValue{}, err
	}
	if len(words) == 2 && words[0] == "-dict-add" {
		// A dict's entry: its key is the name's last part.
		colon := strings.LastIndex(s.Key, ":")
		if colon <= 0 {
			return declaredValue{}, errors.New("-dict-add sets a dict's entry, named as in domain:key:entry")
		}
		s.Key, s.Entry = s.Key[:colon], s.Key[colon+1:]
		words = []string{words[0], s.Entry, words[1]}
	}
	dv := declaredValue{setting: s, words: words}
	last := words[len(words)-1]
	switch words[0] {
	case "-bool", "-boolean":
		switch strings.ToLower(last) {
		case "true", "yes", "1":
			dv.value = true
		case "false", "no", "0":
			dv.value = false
		default:
			return dv, fmt.Errorf("%q isn't true or false", last)
		}
	case "-int", "-integer":
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil {
			return dv, fmt.Errorf("%q isn't a whole number", last)
		}
		dv.value = n
	case "-float":
		f, err := strconv.ParseFloat(last, 64)
		if err != nil {
			return dv, fmt.Errorf("%q isn't a number", last)
		}
		dv.value = f
	case "-string":
		dv.value = last
	default:
		v, err := plist.ReadValue(last)
		if err != nil {
			return dv, fmt.Errorf("its XML doesn't read: %w", err)
		}
		dv.value = v
	}
	return dv, nil
}

// domainID names a domain as exported: currentHost: first for one of this
// host.
func domainID(currentHost bool, domain string) string {
	if currentHost {
		return "currentHost:" + domain
	}
	return domain
}

// export reads a domain's settings.
func (d *Defaults) export(ctx context.Context, currentHost bool, domain string) (map[string]any, error) {
	args := []string{"export", domain, "-"}
	if currentHost {
		args = append([]string{"-currentHost"}, args...)
	}
	res, err := d.run.Run(ctx, runner.Command{Name: "defaults", Args: args})
	if err != nil {
		return nil, err
	}
	v, err := plist.Read(string(res.Stdout))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", domainID(currentHost, domain), err)
	}
	m, _ := v.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// look is what a run of the kind has read: each domain's settings, by
// export.
type look struct {
	domains map[string]map[string]any
}

// read reads the declared domains and the watched ones.
func (d *Defaults) read(ctx context.Context) (look, error) {
	l := look{domains: map[string]map[string]any{}}
	ids := map[string]config.Setting{}
	for _, dv := range d.declared {
		ids[domainID(dv.setting.CurrentHost, dv.setting.Domain)] = dv.setting
	}
	for _, domain := range Watched {
		ids[domain] = config.Setting{Domain: domain}
	}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		s := ids[id]
		m, err := d.export(ctx, s.CurrentHost, s.Domain)
		if err != nil {
			return l, err
		}
		l.domains[id] = m
	}
	return l, nil
}

// actual is a setting's value on the Mac, and whether it's set.
func (l look) actual(s config.Setting) (any, bool) {
	v, ok := l.domains[domainID(s.CurrentHost, s.Domain)][s.Key]
	if !ok || s.Entry == "" {
		return v, ok
	}
	m, _ := v.(map[string]any)
	v, ok = m[s.Entry]
	return v, ok
}

// Installed lists the settings on the Mac kit compares: each declared one
// set as declared, or seen as declared before and changed since (a
// declared one never seen as declared is left out, so it's missing, and
// applying writes it); and each watched one changed since kit took its
// value and not declared.
func (d *Defaults) Installed(ctx context.Context) ([]kind.Installed, error) {
	if !runner.Has(d.run, d.Program()) {
		return nil, fmt.Errorf("defaults: %w", runner.ErrNotFound)
	}
	l, err := d.read(ctx)
	if err != nil {
		return nil, err
	}
	var out []kind.Installed
	err = state.Update(d.stateDir, recordName, func(rec *record) {
		if rec.Seen == nil {
			rec.Seen = map[string]bool{}
		}
		if rec.Domains == nil {
			rec.Domains = map[string]bool{}
		}
		if rec.Baseline == nil {
			rec.Baseline = map[string]string{}
		}
		for _, name := range slices.Sorted(maps.Keys(d.declared)) {
			dv := d.declared[name]
			v, set := l.actual(dv.setting)
			switch {
			case set && plist.Equal(v, dv.value):
				rec.Seen[name] = true
				out = append(out, kind.Installed{Name: name, Explicit: true})
			case rec.Seen[name]:
				out = append(out, kind.Installed{Name: name, Explicit: true})
			}
		}
		for _, name := range d.changed(l, rec) {
			out = append(out, kind.Installed{Name: name, Explicit: true})
		}
	})
	return out, err
}

// watchedSettings are a domain's settings as kit watches them, by name: a
// key's value, or, for a key some of whose entries are declared, each
// entry's.
func (d *Defaults) watchedSettings(id string, m map[string]any) map[string]string {
	s, _ := config.ParseSetting(id + ":x")
	byEntry := map[string]bool{}
	for _, dv := range d.declared {
		if dv.setting.Entry != "" && domainID(dv.setting.CurrentHost, dv.setting.Domain) == id {
			byEntry[dv.setting.Key] = true
		}
	}
	out := map[string]string{}
	for key, v := range m {
		if isNoise(s.Domain, key) {
			continue
		}
		at := config.Setting{CurrentHost: s.CurrentHost, Domain: s.Domain, Key: key}
		if entries, ok := v.(map[string]any); ok && byEntry[key] {
			for entry, ev := range entries {
				at.Entry = entry
				out[at.Name()] = plist.XML(ev)
			}
			continue
		}
		out[at.Name()] = plist.XML(v)
	}
	return out
}

// changed are the watched settings changed since kit took their values, and
// not declared: set, changed or unset. A domain kit hasn't watched before
// has its values taken now, with nothing changed.
func (d *Defaults) changed(l look, rec *record) []string {
	var names []string
	for _, id := range slices.Sorted(maps.Keys(l.domains)) {
		now := d.watchedSettings(id, l.domains[id])
		if !rec.Domains[id] {
			rec.Domains[id] = true
			maps.Copy(rec.Baseline, now)
			continue
		}
		prefix := id + ":"
		for name, was := range rec.Baseline {
			if strings.HasPrefix(name, prefix) && now[name] != was && !d.isDeclared(name) {
				names = append(names, name)
			}
		}
		for name := range now {
			if _, known := rec.Baseline[name]; !known && !d.isDeclared(name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// isDeclared reports whether name, a setting's, is declared.
func (d *Defaults) isDeclared(name string) bool {
	_, ok := d.declared[name]
	return ok
}

// isNoise reports whether key, in domain, is one macOS changes by itself.
func isNoise(domain, key string) bool {
	for _, pattern := range slices.Concat(noise["*"], noise[domain]) {
		if ok, _ := path.Match(pattern, key); ok {
			return true
		}
	}
	return false
}

// Resolve knows every setting: one not set is missing.
func (d *Defaults) Resolve(_ context.Context, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = name
	}
	return out, nil
}

// Differs says, of the declared settings it compares, how each changed since
// kit saw it as declared is set now.
func (d *Defaults) Differs(ctx context.Context, names []string) (map[string]string, error) {
	l, err := d.read(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, name := range names {
		dv := d.declared[name]
		v, set := l.actual(dv.setting)
		switch {
		case !set:
			out[name] = "not set: macOS's default"
		case !plist.Equal(v, dv.value):
			out[name] = "set to " + show(v)
		}
	}
	return out, nil
}

// show is a value, briefly, as a person reads it.
func show(v any) string {
	switch x := v.(type) {
	case bool, int64:
		return fmt.Sprint(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return strconv.Quote(x)
	}
	if s := plist.XML(v); len(s) <= 60 {
		return s
	}
	return "a new value"
}

// Value is a setting's value as it's set, as its declared line takes it.
func (d *Defaults) Value(ctx context.Context, name string) (string, error) {
	s, err := config.ParseSetting(name)
	if err != nil {
		return "", err
	}
	if dv, ok := d.declared[name]; ok {
		s = dv.setting
	} else if colon := strings.LastIndex(s.Key, ":"); colon > 0 && d.entryKey(s.Domain, s.Key[:colon]) {
		s.Key, s.Entry = s.Key[:colon], s.Key[colon+1:]
	}
	v, ok, err := d.valueOf(ctx, s)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "", fmt.Errorf("%s isn't set", name)
	}
	var words []string
	switch x := v.(type) {
	case bool:
		words = []string{"-bool", strconv.FormatBool(x)}
	case int64:
		words = []string{"-int", strconv.FormatInt(x, 10)}
	case float64:
		words = []string{"-float", strconv.FormatFloat(x, 'g', -1, 64)}
	case string:
		words = []string{"-string", x}
	default:
		words = []string{plist.XML(v)}
	}
	if s.Entry != "" {
		words = []string{"-dict-add", plist.XML(v)}
	}
	for i, w := range words {
		words[i] = config.Quote(w)
	}
	return strings.Join(words, " "), nil
}

// entryKey reports whether key, in domain, is declared by its entries.
func (d *Defaults) entryKey(domain, key string) bool {
	for _, dv := range d.declared {
		if dv.setting.Domain == domain && dv.setting.Key == key && dv.setting.Entry != "" {
			return true
		}
	}
	return false
}

// valueOf is a setting's value on the Mac, read now.
func (d *Defaults) valueOf(ctx context.Context, s config.Setting) (any, bool, error) {
	m, err := d.export(ctx, s.CurrentHost, s.Domain)
	if err != nil {
		return nil, false, err
	}
	v, ok := look{domains: map[string]map[string]any{domainID(s.CurrentHost, s.Domain): m}}.actual(s)
	return v, ok, nil
}

// system reports whether domain is the system's, a file under /Library,
// which only an administrator can write.
func system(domain string) bool {
	return strings.HasPrefix(domain, "/Library/")
}

// NeedsAdmin finds the settings, of names, in the system's domains.
func (d *Defaults) NeedsAdmin(_ context.Context, names []string) ([]string, error) {
	var out []string
	for _, name := range names {
		if s, err := config.ParseSetting(name); err == nil && system(s.Domain) {
			out = append(out, name)
		}
	}
	return out, nil
}

// SetAdmin is how the kind finds out whether an administrator's password is
// at hand.
func (d *Defaults) SetAdmin(held func(ctx context.Context) bool) { d.admin = held }

// adminWait says why a system setting is held up.
const adminWait = "waiting for an administrator's password: kit apply at a terminal asks for it"

// Blocked holds up the system settings missing while kit has no
// administrator's password.
func (d *Defaults) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	blocked := map[string]string{}
	if d.admin == nil || d.admin(ctx) {
		return blocked, nil
	}
	for name := range missing {
		if s, err := config.ParseSetting(name); err == nil && system(s.Domain) {
			blocked[name] = adminWait
		}
	}
	return blocked, nil
}

// defaults runs defaults with args for s: through sudo for a system domain,
// with -currentHost for one of this host.
func (d *Defaults) defaults(ctx context.Context, s config.Setting, args ...string) error {
	if s.CurrentHost {
		args = append([]string{"-currentHost"}, args...)
	}
	cmd := runner.Command{Name: "defaults", Args: args}
	if system(s.Domain) {
		cmd = runner.Command{Name: "sudo", Args: append([]string{"-n", "defaults"}, args...)}
	}
	_, err := d.run.Run(ctx, cmd)
	return err
}

// Install writes each declared setting, then has the apps that read their
// domains take them up.
func (d *Defaults) Install(ctx context.Context, names []string) error {
	var errs []error
	var domains []string
	for _, name := range names {
		dv, ok := d.declared[name]
		if !ok {
			errs = append(errs, fmt.Errorf("%s isn't declared", name))
			continue
		}
		args := slices.Concat([]string{"write", dv.setting.Domain, dv.setting.Key}, dv.words)
		if err := d.defaults(ctx, dv.setting, args...); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		domains = append(domains, dv.setting.Domain)
	}
	return errors.Join(append(errs, d.takeUp(ctx, domains))...)
}

// Remove unsets each setting: macOS's default comes back.
func (d *Defaults) Remove(ctx context.Context, names []string) error {
	var errs []error
	var domains []string
	for _, name := range names {
		if err := d.write(ctx, name, nil, false); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		s, _ := config.ParseSetting(name)
		domains = append(domains, s.Domain)
	}
	return errors.Join(append(errs, d.takeUp(ctx, domains))...)
}

// Revert puts a watched setting changed on the Mac back as kit last
// accepted it: its old value, or unset when it wasn't set.
func (d *Defaults) Revert(ctx context.Context, name string) error {
	rec, err := state.Load[record](d.stateDir, recordName)
	if err != nil {
		return err
	}
	was, set := rec.Baseline[name]
	var v any
	if set {
		if v, err = plist.ReadValue(was); err != nil {
			return err
		}
	}
	if err := d.write(ctx, name, v, set); err != nil {
		return err
	}
	s, _ := config.ParseSetting(name)
	return d.takeUp(ctx, []string{s.Domain})
}

// write sets the setting name to v, or unsets it when set is false: a
// dict's entry by writing the dict whole.
func (d *Defaults) write(ctx context.Context, name string, v any, set bool) error {
	s, err := config.ParseSetting(name)
	if err != nil {
		return err
	}
	if colon := strings.LastIndex(s.Key, ":"); colon > 0 && d.entryKey(s.Domain, s.Key[:colon]) {
		s.Key, s.Entry = s.Key[:colon], s.Key[colon+1:]
	}
	if s.Entry == "" {
		if !set {
			err := d.defaults(ctx, s, "delete", s.Domain, s.Key)
			if _, unset := errors.AsType[*runner.ExitError](err); unset {
				// It wasn't set.
				return nil
			}
			return err
		}
		return d.defaults(ctx, s, "write", s.Domain, s.Key, plist.XML(v))
	}
	whole, _, err := d.valueOf(ctx, config.Setting{CurrentHost: s.CurrentHost, Domain: s.Domain, Key: s.Key})
	if err != nil {
		return err
	}
	m, _ := whole.(map[string]any)
	m = maps.Clone(m)
	if m == nil {
		m = map[string]any{}
	}
	if set {
		m[s.Entry] = v
	} else {
		delete(m, s.Entry)
	}
	return d.defaults(ctx, s, "write", s.Domain, s.Key, plist.XML(m))
}

// takeUp has the apps that read domains take their new settings up: the
// Dock, Finder, Control Center and the menu bar restarted, keyboard
// shortcuts reloaded.
func (d *Defaults) takeUp(ctx context.Context, domains []string) error {
	restart := map[string]string{
		"com.apple.dock":            "Dock",
		"com.apple.spaces":          "Dock",
		"com.apple.WindowManager":   "Dock",
		"com.apple.finder":          "Finder",
		"com.apple.controlcenter":   "ControlCenter",
		"com.apple.menuextra.clock": "ControlCenter",
		"com.apple.screencapture":   "SystemUIServer",
	}
	var apps []string
	reload := false
	for _, domain := range domains {
		if app, ok := restart[domain]; ok {
			apps = append(apps, app)
		}
		reload = reload || domain == "com.apple.symbolichotkeys"
	}
	slices.Sort(apps)
	var errs []error
	for _, app := range slices.Compact(apps) {
		// Not running is no matter: it reads the setting when it starts.
		_, _ = d.run.Run(ctx, runner.Command{Name: "killall", Args: []string{app}})
	}
	if reload {
		if _, err := d.run.Run(ctx, runner.Command{Name: activateSettings, Args: []string{"-u"}}); err != nil {
			errs = append(errs, fmt.Errorf("reload keyboard shortcuts: %w", err))
		}
	}
	return errors.Join(errs...)
}

// activateSettings has macOS take up keyboard shortcuts' settings.
const activateSettings = "/System/Library/PrivateFrameworks/SystemAdministration.framework/Resources/activateSettings"

var _ interface {
	kind.Kind
	kind.Valued
	kind.Diverger
	kind.Admin
	kind.Blocker
	kind.Reverter
} = (*Defaults)(nil)
