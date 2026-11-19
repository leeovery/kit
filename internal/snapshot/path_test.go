package snapshot_test

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/snapshot"
)

// onPath is fake as a runner on kit's PATH with dirs last on it: each
// program of at, by the folders it's in, found only once one of them is on
// it, as a real runner finds it; the rest as fake finds them.
type onPath struct {
	*runnertest.Fake
	dirs []string
	at   map[string][]string
}

func (p onPath) Has(name string) bool {
	if in, ok := p.at[name]; ok {
		return slices.ContainsFunc(in, func(dir string) bool { return slices.Contains(p.dirs, dir) })
	}
	return p.Fake.Has(name)
}

// alongPath is m, its programs in the folders programs gives, off kit's
// PATH, looked for along beyond.
func alongPath(m snapshot.Mac, fake *runnertest.Fake, programs map[string][]string, beyond ...string) snapshot.Mac {
	m.Run = onPath{Fake: fake, at: programs}
	m.RunOn = func(dirs []string) runner.Runner { return onPath{Fake: fake, dirs: dirs, at: programs} }
	m.Beyond = beyond
	return m
}

// A kind whose program is off kit's PATH, on the one setup was run with, is
// looked at with its folder last on kit's PATH, and the folder declared in
// the Mac's [paths], as kit path add declares it, ~ for the home: each
// folder once, only those a program needed, in the order looked along, so
// the shell's comes before Homebrew's or the system's. A folder named for
// a version is passed over for one further along.
func TestAProgramOffKitsPathDeclaresItsFolder(t *testing.T) {
	const homebrew, shims, local = "/opt/homebrew/bin", "~/.local/share/mise/shims", "/usr/local/bin"
	tests := []struct {
		name     string
		programs map[string][]string
		beyond   []string
		want     []string
	}{
		{name: "on the shell's PATH alone", programs: map[string][]string{"npm": {shims}},
			beyond: []string{shims, "~/.npm-global/bin", homebrew}, want: []string{shims}},
		{name: "the shell's first", programs: map[string][]string{"npm": {shims, local}},
			beyond: []string{shims, homebrew, local}, want: []string{shims}},
		{name: "past a folder named for a version", programs: map[string][]string{"npm": {"~/.nvm/versions/node/v22.11.0/bin", shims}},
			beyond: []string{"~/.nvm/versions/node/v22.11.0/bin", shims}, want: []string{shims}},
		{name: "the home itself", programs: map[string][]string{"npm": {"~"}},
			beyond: []string{"~"}, want: []string{"~"}},
		{name: "two programs in one folder", programs: map[string][]string{"npm": {shims}, "composer": {shims}},
			beyond: []string{shims}, want: []string{shims}},
		{name: "each in its own folder", programs: map[string][]string{"npm": {shims}, "composer": {"/usr/local/bin"}},
			beyond: []string{"/usr/local/bin", "~/bin", shims}, want: []string{"/usr/local/bin", shims}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, fake := newMac(t, "")
			full := func(dirs []string) []string {
				var out []string
				for _, dir := range dirs {
					if rest, ok := strings.CutPrefix(dir, "~"); ok {
						dir = filepath.Join(m.Home, rest)
					}
					out = append(out, dir)
				}
				return out
			}
			programs := map[string][]string{}
			for program, dirs := range tt.programs {
				programs[program] = full(dirs)
			}
			fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {"typescript": {"version": "5.9.3"}}}`)
			fake.On("composer", "global", "show", "--direct", "--format=json").Prints(`{"installed": []}`)
			m = alongPath(m, fake, programs, full(tt.beyond)...)
			var mu sync.Mutex
			var told []snapshot.Look
			m.Looks = func(l snapshot.Look) {
				mu.Lock()
				defer mu.Unlock()
				told = append(told, l)
			}

			s, looked, err := snapshot.Find(t.Context(), m)
			if err != nil {
				t.Fatal(err)
			}
			if err := snapshot.Write(m, &s); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range s.Parts[0].Lines {
				got = append(got, e.Name)
			}
			if s.Parts[0].Kind != "paths" || s.Parts[0].Label != snapshot.PathsLabel || !slices.Equal(got, tt.want) {
				t.Errorf("first part = %s %q, want paths %q", s.Parts[0].Kind, got, tt.want)
			}
			if c := counts(s); !slices.Contains(c, "npm 1") {
				t.Errorf("parts = %q, want npm's package found once its folder's on the PATH", c)
			}
			if l := told[0]; l.Label != snapshot.PathsLabel || !l.Looked || l.Found != len(tt.want) || !slices.Equal(l.Names, tt.want) {
				t.Errorf("first told %+v, want the PATH's folders, by name", l)
			}
			if dirs := looked.Run.(onPath).dirs; !slices.Equal(dirs, full(tt.want)) {
				t.Errorf("looked with %q last on the PATH, want %q", dirs, full(tt.want))
			}
			if got := declarations(t, m); !strings.Contains(got, "[paths]\n"+strings.Join(tt.want, "\n")+"\n") {
				t.Errorf("laptop/declarations =\n%s\nwant [paths] holding %q", got, tt.want)
			}
		})
	}
}

// Never on the PATH: a folder of kit's own, a temporary one, or one that
// isn't absolute, which only a shell in a project folder finds anything
// in; and nothing for a program kit's PATH finds, as it does once
// kit-config declares its folder.
func TestFoldersNeverDeclaredOnThePath(t *testing.T) {
	tests := []struct {
		name string
		dir  func(m snapshot.Mac) string
	}{
		{"kit's state", func(m snapshot.Mac) string { return filepath.Join(m.Dirs.State, "bin") }},
		{"kit's data", func(m snapshot.Mac) string { return m.Dirs.Data }},
		{"kit-config", func(m snapshot.Mac) string { return filepath.Join(m.Dirs.Config, "bin") }},
		{"a temporary folder", func(m snapshot.Mac) string { return filepath.Join(m.Temp[0], "go-build1", "bin") }},
		{"not absolute", func(snapshot.Mac) string { return "node_modules/.bin" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, fake := newMac(t, "")
			m.Temp = []string{t.TempDir()}
			dir := tt.dir(m)
			m = alongPath(m, fake, map[string][]string{"npm": {dir}}, dir)
			s, _, err := snapshot.Find(t.Context(), m)
			if c := counts(s); err != nil || !slices.Equal(c, []string{"defaults 0", "features 0", "backups 0"}) {
				t.Errorf("parts = %q, %v; want none for npm, nor the PATH", c, err)
			}
		})
	}

	t.Run("on kit's PATH already", func(t *testing.T) {
		m, fake := newMac(t, "[paths]\n~/.local/share/mise/shims\n")
		fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {}}`)
		m = alongPath(m, fake, nil, filepath.Join(m.Home, ".local", "share", "mise", "shims"))
		s, _, err := snapshot.Find(t.Context(), m)
		if c := counts(s); err != nil || !slices.Equal(c, []string{"npm 0", "defaults 0", "features 0", "backups 0"}) {
			t.Errorf("parts = %q, %v; want npm's, and nothing for the PATH", c, err)
		}
	})
}

// A folder named for a version, as a version manager keeps each version
// in, is never declared, as the next upgrade takes it away: when it alone
// holds a kind's program, the PATH's part refuses it, saying what it
// holds and that a folder that lasts is for kit path add.
func TestAFolderNamedForAVersionIsRefused(t *testing.T) {
	for _, dir := range []string{"~/.nvm/versions/node/v22.11.0/bin", "~/.local/share/mise/installs/node/22.11.0/bin"} {
		t.Run(dir, func(t *testing.T) {
			m, fake := newMac(t, "")
			full := filepath.Join(m.Home, strings.TrimPrefix(dir, "~/"))
			m = alongPath(m, fake, map[string][]string{"npm": {full}}, full)
			s, _, err := snapshot.Find(t.Context(), m)
			if err != nil {
				t.Fatal(err)
			}
			if c := counts(s); !slices.Equal(c, []string{"paths 0", "defaults 0", "features 0", "backups 0"}) {
				t.Errorf("parts = %q, want the PATH's, declaring nothing, and none for npm", c)
			}
			refused := s.Parts[0].Refused
			if len(refused) != 1 || refused[0].Entry.Name != dir || !strings.Contains(refused[0].Err.Error(), "holds npm") || !strings.Contains(refused[0].Err.Error(), "kit path add") {
				t.Errorf("refused = %+v, want %s, saying it holds npm and naming kit path add", refused, dir)
			}
		})
	}
}
