// Package mcp is the kind of Claude Code's MCP servers: the user-level ones,
// and each project folder's own, in local scope, private to the user.
// They're declared in the config repository's mcp.json and mcp.<mac>.json
// (a server's name and definition, exactly what claude mcp add-json takes;
// a key naming a project folder holds that project's servers), and found in
// ~/.claude.json, read directly: claude mcp list starts every server to
// check it. Keys are never declared, only named as ${VAR}, which Claude Code
// fills from its environment.
package mcp

import (
	"bytes"
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

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// notHere is why a project's server waits.
const notHere = "the folder isn't here yet"

// MCP is Claude Code's MCP servers, driven through a runner.
type MCP struct {
	run runner.Runner
	// home is the user's home, where ~/.claude.json is.
	home string
	// dir is the config repository, mac this Mac's name, and macs every
	// Mac's it knows.
	dir, mac string
	macs     []string
	// declared are the servers declared for this Mac, by id, once Declared
	// has read them.
	declared map[string]Server
}

// New returns Claude Code's MCP servers, driven through run, for the user
// whose home is home, as the config repository in dir declares them for
// the Mac named mac, one of macs.
func New(run runner.Runner, home, dir, mac string, macs []string) *MCP {
	return &MCP{run: run, home: home, dir: dir, mac: mac, macs: macs, declared: map[string]Server{}}
}

func (*MCP) Name() string    { return "mcp" }
func (*MCP) Title() string   { return "MCP servers" }
func (*MCP) Program() string { return "claude" }

// Declared reads the servers declared for this Mac: the shared file's, then
// the Mac's own. A server may be in one of them, once.
func (m *MCP) Declared() (config.List, error) {
	list := config.List{Kind: m.Name()}
	m.declared = make(map[string]Server)
	in := make(map[string]string)
	for _, file := range []string{FileName(""), FileName(m.mac)} {
		servers, err := readFile(filepath.Join(m.dir, file))
		if err != nil {
			return config.List{Kind: m.Name()}, err
		}
		for _, s := range servers {
			if other, ok := in[s.ID()]; ok {
				return config.List{Kind: m.Name()}, fmt.Errorf("%s: %s is in %s too: a server goes in the shared file or a Mac's, not both", file, s.ID(), other)
			}
			in[s.ID()] = file
			m.declared[s.ID()] = s
			list.Entries = append(list.Entries, config.Entry{Name: s.ID(), File: file, Line: s.Line, Note: s.Note, Off: s.Off})
		}
	}
	return list, nil
}

// FileFor is the file a server's declared in: the one every Mac reads, or
// this Mac's own.
func (m *MCP) FileFor(shared bool) string {
	if shared {
		return FileName("")
	}
	return FileName(m.mac)
}

// Where are the entries declaring the server name, in every Mac's files.
func (m *MCP) Where(name string) ([]config.Entry, error) {
	var found []config.Entry
	for _, file := range m.files() {
		servers, err := readFile(filepath.Join(m.dir, file))
		if err != nil {
			return nil, err
		}
		for _, s := range servers {
			if s.ID() == name {
				found = append(found, config.Entry{Name: s.ID(), File: file, Line: s.Line, Note: s.Note, Off: s.Off})
			}
		}
	}
	return found, nil
}

// files are every Mac's declarations files: the shared one, then each
// Mac's, by name.
func (m *MCP) files() []string {
	files := []string{FileName("")}
	for _, mac := range m.macs {
		files = append(files, FileName(mac))
	}
	return files
}

// Adopt declares the server name, as it's installed, in file, with note. A
// server holding a key in plain text isn't declared: the key goes in
// 1Password first, named in its definition as ${VAR}.
func (m *MCP) Adopt(_ context.Context, file, name, note string) error {
	servers, err := m.installed()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(servers, func(s Server) bool { return s.ID() == name })
	if i < 0 {
		return fmt.Errorf("%s isn't installed in Claude Code", name)
	}
	s := servers[i]
	if plain := PlainKeys(s.Definition); len(plain) > 0 {
		return fmt.Errorf("%s holds a key in plain text (%s): put the key in 1Password, name it in the server's definition as ${VAR}, then declare it", name, strings.Join(plain, " and "))
	}
	s.Definition, s.Note = normal(s.Definition), note
	return Declare(m.dir, file, s)
}

// Undeclare takes the server name out of file.
func (m *MCP) Undeclare(file, name string) error {
	return Undeclare(m.dir, file, name)
}

// SetOff declares the server name, in file, off or on.
func (m *MCP) SetOff(file, name string, off bool) error {
	return SetOff(m.dir, file, name, off)
}

// Installed lists the servers installed, by id, each installed for itself.
func (m *MCP) Installed(context.Context) ([]kind.Installed, error) {
	if !runner.Has(m.run, m.Program()) {
		return nil, fmt.Errorf("claude: %w", runner.ErrNotFound)
	}
	servers, err := m.installed()
	if err != nil {
		return nil, err
	}
	installed := make([]kind.Installed, len(servers))
	for i, s := range servers {
		installed[i] = kind.Installed{Name: s.ID(), Explicit: true}
	}
	return installed, nil
}

// installed reads the servers installed from ~/.claude.json: the user
// scope's, and each project's, in order of their ids.
func (m *MCP) installed() ([]Server, error) {
	path := filepath.Join(m.home, ".claude.json")
	var file struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		} `json:"projects"`
	}
	for tries := 0; ; tries++ {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read ~/.claude.json: %w", err)
		}
		err = decode(data, &file)
		if err == nil {
			break
		}
		if tries == 2 {
			return nil, fmt.Errorf("~/.claude.json: %w", err)
		}
		// Claude Code may be writing it.
		time.Sleep(200 * time.Millisecond)
	}
	var servers []Server
	for name, def := range file.MCPServers {
		servers = append(servers, Server{Name: name, Definition: def})
	}
	for path, project := range file.Projects {
		for name, def := range project.MCPServers {
			servers = append(servers, Server{Folder: m.shown(path), Name: name, Definition: def})
		}
	}
	slices.SortFunc(servers, func(a, b Server) int { return strings.Compare(a.ID(), b.ID()) })
	return servers, nil
}

// Resolve takes every name as a server's id.
func (*MCP) Resolve(_ context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		resolved[name] = name
	}
	return resolved, nil
}

// Differs says, of the declared servers installed, how each installed
// otherwise than declared differs: which parts of its definition.
func (m *MCP) Differs(_ context.Context, names []string) (map[string]string, error) {
	servers, err := m.installed()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Server, len(servers))
	for _, s := range servers {
		byID[s.ID()] = s
	}
	differs := make(map[string]string)
	for _, name := range names {
		d, ok := m.declared[name]
		i, there := byID[name]
		if !ok || !there {
			continue
		}
		if fields := differing(normal(d.Definition), normal(i.Definition)); len(fields) > 0 {
			differs[name] = "installed differently: " + strings.Join(fields, ", ")
		}
	}
	return differs, nil
}

// Blocked holds up the project servers whose folders aren't on the Mac.
func (m *MCP) Blocked(_ context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	blocked := make(map[string]string)
	for name := range missing {
		if s, ok := m.declared[name]; ok && s.Folder != "" && !exists(m.expand(s.Folder)) {
			blocked[name] = notHere
		}
	}
	return blocked, nil
}

// Install installs each server named as declared, replacing one installed
// otherwise.
func (m *MCP) Install(ctx context.Context, names []string) error {
	servers, err := m.installed()
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		s, ok := m.declared[name]
		if !ok {
			errs = append(errs, fmt.Errorf("%s isn't declared: add it to Claude Code with claude mcp add, and kit add mcp declares it", name))
			continue
		}
		if slices.ContainsFunc(servers, func(i Server) bool { return i.ID() == name }) {
			if err := m.claude(ctx, s, "remove", s.Name); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		def, err := canonical(s.Definition)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, m.claude(ctx, s, "add-json", s.Name, def))
	}
	return errors.Join(errs...)
}

// Remove removes each server named from Claude Code.
func (m *MCP) Remove(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		folder, server := splitID(name)
		errs = append(errs, m.claude(ctx, Server{Folder: folder, Name: server}, "remove", server))
	}
	return errors.Join(errs...)
}

// claude runs claude mcp verb, in the user's scope for a user-level server,
// or in local scope in its folder for a project's: the scope goes before
// the rest of args.
func (m *MCP) claude(ctx context.Context, s Server, verb string, args ...string) error {
	scope := "user"
	cmd := runner.Command{Name: m.Program()}
	if s.Folder != "" {
		scope, cmd.Dir = "local", m.expand(s.Folder)
	}
	cmd.Args = slices.Concat([]string{"mcp", verb, "-s", scope}, args)
	_, err := m.run.Run(ctx, cmd)
	return err
}

// shown is a path as written in the config: ~ for the home.
func (m *MCP) shown(path string) string {
	if rest, ok := strings.CutPrefix(path, m.home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// expand is a folder as written, with ~ for the home, as a path.
func (m *MCP) expand(folder string) string {
	if rest, ok := strings.CutPrefix(folder, "~/"); ok {
		return filepath.Join(m.home, rest)
	}
	return folder
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// normal is a definition as it compares: a server with a command and no
// type is stdio, and an empty field counts as unset.
func normal(def map[string]any) map[string]any {
	out := make(map[string]any, len(def))
	for k, v := range def {
		switch t := v.(type) {
		case map[string]any:
			if len(t) == 0 {
				continue
			}
		case []any:
			if len(t) == 0 {
				continue
			}
		case string:
			if t == "" {
				continue
			}
		}
		out[k] = v
	}
	if _, typed := out["type"]; !typed && out["command"] != nil {
		out["type"] = "stdio"
	}
	return out
}

// differing are the fields a and b differ in, sorted.
func differing(a, b map[string]any) []string {
	keys := make(map[string]bool, len(a)+len(b))
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var fields []string
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		x, _ := canonical(a[k])
		y, _ := canonical(b[k])
		if x != y {
			fields = append(fields, k)
		}
	}
	return fields
}

// canonical is v as JSON: keys sorted, compact, nothing escaped that needn't
// be.
func canonical(v any) (string, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
