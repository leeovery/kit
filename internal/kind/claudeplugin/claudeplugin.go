// Package claudeplugin is the kind of Claude Code's plugins, at user scope.
// A plugin is named by its marketplace's repository, as in
// revdiff@umputun/revdiff, the way a formula's name carries its tap: there's
// no list of marketplaces, as kit adds a plugin's when it installs it. A
// marketplace kept with no plugin from it is named @owner/repo. Claude Code's
// own records say what's installed: ~/.claude/plugins/installed_plugins.json,
// known_marketplaces.json, and the plugins enabled in ~/.claude/settings.json.
package claudeplugin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// installTimeout is how long installing a plugin, or adding a marketplace,
// may take: each is a git clone.
const installTimeout = 5 * time.Minute

// Plugins is Claude Code's plugins, driven through a runner.
type Plugins struct {
	run runner.Runner
	// home is the user's home, where ~/.claude is.
	home string
}

// New returns Claude Code's plugins, driven through run, for the user whose
// home is home.
func New(run runner.Runner, home string) *Plugins {
	return &Plugins{run: run, home: home}
}

func (*Plugins) Name() string    { return "claude-plugin" }
func (*Plugins) Title() string   { return "Claude plugins" }
func (*Plugins) Program() string { return "claude" }

// records are what Claude Code records of its plugins.
type records struct {
	// plugins are those installed at user scope, as Claude Code calls them:
	// plugin@marketplace.
	plugins []string
	// sources are each marketplace's source, by its name: a GitHub
	// repository, else a URL or a path.
	sources map[string]string
	// disabled are the plugins settings.json turns off.
	disabled map[string]bool
}

// install is a plugin's install, as Claude Code records it: at user,
// project or local scope.
type install struct {
	Scope string `json:"scope"`
}

// read reads Claude Code's records: none, where a file isn't there yet.
func (p *Plugins) read() (records, error) {
	r := records{sources: map[string]string{}, disabled: map[string]bool{}}
	var installed struct {
		Plugins map[string][]install `json:"plugins"`
	}
	if err := p.load(filepath.Join(".claude", "plugins", "installed_plugins.json"), &installed); err != nil {
		return r, err
	}
	for id, installs := range installed.Plugins {
		if slices.ContainsFunc(installs, func(i install) bool { return i.Scope == "user" }) {
			r.plugins = append(r.plugins, id)
		}
	}
	slices.Sort(r.plugins)
	var markets map[string]struct {
		Source struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
			URL    string `json:"url"`
			Path   string `json:"path"`
		} `json:"source"`
	}
	if err := p.load(filepath.Join(".claude", "plugins", "known_marketplaces.json"), &markets); err != nil {
		return r, err
	}
	for name, m := range markets {
		r.sources[name] = cmp.Or(m.Source.Repo, m.Source.URL, m.Source.Path, name)
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := p.load(filepath.Join(".claude", "settings.json"), &settings); err != nil {
		return r, err
	}
	for id, on := range settings.EnabledPlugins {
		if !on {
			r.disabled[id] = true
		}
	}
	return r, nil
}

// load reads the JSON file at path, from the home, into v: nothing, when
// there's no such file.
func (p *Plugins) load(path string, v any) error {
	data, err := os.ReadFile(filepath.Join(p.home, path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read ~/%s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("~/%s: %w", path, err)
	}
	return nil
}

// named is a plugin as kit names it: by its marketplace's source.
func (r records) named(id string) string {
	plugin, market, _ := strings.Cut(id, "@")
	return plugin + "@" + cmp.Or(r.sources[market], market)
}

// market is the name Claude Code knows the marketplace of source by: ""
// when it hasn't it.
func (r records) market(source string) string {
	for _, name := range slices.Sorted(maps.Keys(r.sources)) {
		if strings.EqualFold(r.sources[name], source) {
			return name
		}
	}
	return ""
}

// split splits a name as kit has it into the plugin, "" for a marketplace
// alone, and its marketplace's source.
func split(name string) (plugin, source string) {
	plugin, source, _ = strings.Cut(name, "@")
	return plugin, source
}

// Installed lists the plugins installed at user scope, each for itself, and
// the marketplaces, each needed while an installed plugin comes from it.
func (p *Plugins) Installed(context.Context) ([]kind.Installed, error) {
	if !runner.Has(p.run, p.Program()) {
		return nil, fmt.Errorf("claude: %w", runner.ErrNotFound)
	}
	r, err := p.read()
	if err != nil {
		return nil, err
	}
	used := make(map[string]bool)
	var installed []kind.Installed
	for _, id := range r.plugins {
		_, market, _ := strings.Cut(id, "@")
		used[market] = true
		installed = append(installed, kind.Installed{Name: r.named(id), Explicit: true})
	}
	for _, name := range slices.Sorted(maps.Keys(r.sources)) {
		installed = append(installed, kind.Installed{Name: "@" + r.sources[name], Needed: used[name]})
	}
	return installed, nil
}

// Key is a name in lowercase, as GitHub takes repositories' names.
func (*Plugins) Key(name string) string {
	return strings.ToLower(name)
}

// Resolve takes every name as a plugin's: one that isn't fails to install.
func (*Plugins) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Differs finds, of the declared plugins installed, those turned off.
func (p *Plugins) Differs(_ context.Context, names []string) (map[string]string, error) {
	r, err := p.read()
	if err != nil {
		return nil, err
	}
	differs := make(map[string]string)
	for _, name := range names {
		plugin, source := split(name)
		if plugin == "" {
			continue
		}
		if market := r.market(source); market != "" && r.disabled[plugin+"@"+market] {
			differs[name] = "installed differently: disabled"
		}
	}
	return differs, nil
}

// Install installs each plugin named, adding its marketplace first when
// Claude Code hasn't it, or turns it on when it's installed but off; a
// marketplace alone is added.
func (p *Plugins) Install(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		errs = append(errs, p.installOne(ctx, name))
	}
	return errors.Join(errs...)
}

func (p *Plugins) installOne(ctx context.Context, name string) error {
	plugin, source := split(name)
	r, err := p.read()
	if err != nil {
		return err
	}
	market := r.market(source)
	if market == "" {
		if err := p.claude(ctx, installTimeout, "plugin", "marketplace", "add", source); err != nil {
			return err
		}
		if r, err = p.read(); err != nil {
			return err
		}
		if market = r.market(source); market == "" {
			return fmt.Errorf("claude added %s, yet doesn't list it", source)
		}
	}
	if plugin == "" {
		return nil
	}
	id := plugin + "@" + market
	if slices.Contains(r.plugins, id) {
		return p.claude(ctx, 0, "plugin", "enable", id, "--scope", "user")
	}
	return p.claude(ctx, installTimeout, "plugin", "install", id, "--scope", "user")
}

// Remove uninstalls each plugin named, or removes each marketplace.
func (p *Plugins) Remove(ctx context.Context, names []string) error {
	r, err := p.read()
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		plugin, source := split(name)
		market := r.market(source)
		switch {
		case market == "":
			errs = append(errs, fmt.Errorf("claude has no marketplace from %s", source))
		case plugin == "":
			errs = append(errs, p.claude(ctx, 0, "plugin", "marketplace", "remove", market))
		default:
			errs = append(errs, p.claude(ctx, 0, "plugin", "uninstall", plugin+"@"+market, "--scope", "user"))
		}
	}
	return errors.Join(errs...)
}

func (p *Plugins) claude(ctx context.Context, timeout time.Duration, args ...string) error {
	_, err := p.run.Run(ctx, runner.Command{Name: p.Program(), Args: args, Timeout: timeout})
	return err
}
