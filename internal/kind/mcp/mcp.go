// Package mcp is the kind of Claude Code's MCP servers: the user-level ones,
// and each project folder's own, in local scope, private to the user.
// They're declared in the config repository's [claude mcp] sections, each a
// line: the server's name, then claude mcp add's options (or JSON for what
// they can't say); a project folder's in its own section, [claude mcp
// ~/Code/site]. They're found in ~/.claude.json, read directly: claude mcp
// list starts every server to check it. Keys are never declared, only named
// as ${VAR}, which Claude Code fills from its environment.
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

// Server is an MCP server as kit declares it, or finds it installed.
type Server struct {
	// Folder is the project folder whose server it is, as written, as in
	// ~/Code/site: "" for a user-level one.
	Folder string
	Name   string
	// Definition is what claude mcp add-json takes.
	Definition map[string]any
	// Off is whether it's declared, but not to be installed.
	Off bool
}

// ID is what kit calls the server: its name, or, for a project's, the
// folder and the name, as in ~/Code/site:resend.
func (s Server) ID() string {
	if s.Folder == "" {
		return s.Name
	}
	return s.Folder + ":" + s.Name
}

// splitID splits a server's id into its folder, "" for a user-level
// server, and its name.
func splitID(id string) (folder, name string) {
	if strings.HasPrefix(id, "~/") || strings.HasPrefix(id, "/") {
		if i := strings.LastIndex(id, ":"); i > 0 {
			return id[:i], id[i+1:]
		}
	}
	return "", id
}

// MCP is Claude Code's MCP servers, driven through a runner.
type MCP struct {
	run runner.Runner
	// home is the user's home, where ~/.claude.json is.
	home string
	// declared are the servers declared for this Mac, by id, once Values
	// has read them.
	declared map[string]Server
}

// New returns Claude Code's MCP servers, driven through run, for the user
// whose home is home.
func New(run runner.Runner, home string) *MCP {
	return &MCP{run: run, home: home, declared: map[string]Server{}}
}

func (*MCP) Name() string    { return "claude-mcp" }
func (*MCP) Title() string   { return "Claude MCP servers" }
func (*MCP) Program() string { return "claude" }

// Values reads each declared server's line into its definition, marking
// those declared off. A line that doesn't read, or holds a key in plain
// text, is refused.
func (m *MCP) Values(list config.List) (config.List, error) {
	m.declared = make(map[string]Server, len(list.Entries))
	out := list
	out.Entries = slices.Clone(list.Entries)
	for i, e := range out.Entries {
		def, isOff, err := readValue(e.Value)
		if err != nil {
			return list, fmt.Errorf("%s: %s: %w", e.Pos(), e.Name, err)
		}
		if plain := PlainKeys(def); len(plain) > 0 {
			return list, fmt.Errorf("%s: %s: %s holds a key in plain text: put the key in 1Password, and name it here as ${VAR}", e.Pos(), e.Name, strings.Join(plain, " and "))
		}
		folder, name := splitID(e.Name)
		m.declared[e.Name] = Server{Folder: folder, Name: name, Definition: def, Off: isOff}
		out.Entries[i].Off = isOff
	}
	return out, nil
}

// Value is the line to declare the server name with, after its name, as
// Claude Code has it, its empty fields left out. A server holding a key in
// plain text isn't declared: the key goes in 1Password first, named in its
// definition as ${VAR}.
func (m *MCP) Value(_ context.Context, name string) (string, error) {
	servers, err := m.installed()
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(servers, func(s Server) bool { return s.ID() == name })
	if i < 0 {
		return "", fmt.Errorf("%s isn't installed in Claude Code", name)
	}
	if plain := PlainKeys(servers[i].Definition); len(plain) > 0 {
		return "", fmt.Errorf("%s holds a key in plain text (%s): put the key in 1Password, name it in the server's definition as ${VAR}, then declare it", name, strings.Join(plain, " and "))
	}
	return writeValue(normal(servers[i].Definition), false)
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
			errs = append(errs, fmt.Errorf("%s isn't declared: add it to Claude Code with claude mcp add, and kit claude-mcp add declares it", name))
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

// decode decodes JSON data into v, numbers kept as they're written, and
// nothing after the value.
func decode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("not JSON kit reads: %w", err)
	}
	if d.More() {
		return errors.New("not JSON kit reads: something after the object")
	}
	return nil
}
