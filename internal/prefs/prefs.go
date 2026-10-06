// Package prefs is kit's saving and restoring of apps' settings, the
// successor to the dotfiles' prefsync: every preferences domain that isn't
// Apple's or the system's, through defaults export and import (cfprefsd's
// view, never a plist copied behind its back), and the settings files
// [prefs files] names. Each Mac keeps them in its own folder of one git
// repository (kit.toml's prefs_repo), cloned in kit's data folder: a commit
// for each capture that changed anything, pushed, so history can restore
// the settings as they were on a date.
//
// The store's format is prefsync's, byte for byte: <mac>/domains/<domain>.plist
// in sorted XML, keys that change by themselves stripped; <mac>/files/
// mirroring each file's path under the home folder (or _root/ for one
// outside it). <mac>/owner records the hardware of the Mac the folder
// belongs to: another Mac, such as a replacement given the same name, never
// captures into it, and settings bound to one Mac are never restored onto
// other hardware.
package prefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// recordName names kit's record of capture and restore on this Mac.
const recordName = "prefs.json"

// backupsDir is the folder, in kit's state, of the live settings saved
// before a restore replaced them.
const backupsDir = "prefs-backups"

// Record is kit's record of capture and restore on this Mac.
type Record struct {
	// On says capture is switched on for this Mac, and how: by a full
	// restore, or by starting fresh. A rebuilt Mac has no record, so never
	// captures fresh defaults over a good store before it's restored.
	On string `json:"on,omitempty"`
	// Captured is when the last capture ran, and Summary what it found.
	Captured time.Time `json:"captured,omitzero"`
	Summary  string    `json:"summary,omitempty"`
	// Errors counts what the last capture couldn't save.
	Errors int `json:"errors,omitempty"`
	// Unpushed is since when commits have waited to be pushed, and
	// PushError why the last push failed: none when all's pushed.
	Unpushed  time.Time `json:"unpushed,omitzero"`
	PushError string    `json:"push_error,omitempty"`
	// Pending are the domains waiting to be restored, from the Mac From.
	Pending *Pending `json:"pending,omitempty"`
	// Restored is the last full restore: from which Mac, its commit, when.
	Restored *Restored `json:"restored,omitempty"`
	// NotOwner says why the last capture was refused: the folder belongs to
	// another Mac.
	NotOwner string `json:"not_owner,omitempty"`
}

// Pending are domains waiting to be restored: their apps weren't installed,
// or were running, or the restore failed.
type Pending struct {
	From    string   `json:"from"`
	Domains []string `json:"domains"`
}

// Restored is where a full restore came from.
type Restored struct {
	From   string    `json:"from"`
	Commit string    `json:"commit,omitempty"`
	At     time.Time `json:"at"`
}

// Lists are kit-config's lists for prefs: the settings files saved besides
// the domains; the domains never saved; the apps domains belong to when
// their names don't say, each a pattern and a bundle id; and the settings
// bound to one Mac.
type Lists struct {
	Files        []string
	Deny         []string
	Apps         [][2]string
	MachineBound []string
}

// Prefs is apps' settings on this Mac, and their store.
type Prefs struct {
	Run  runner.Runner
	Home string
	// State is kit's state folder, holding the record and saved settings.
	State string
	// Clone is the store's clone; Remote, where it's cloned from.
	Clone  string
	Remote string
	// Mac is this Mac's name: its folder in the store.
	Mac   string
	Lists Lists
	Now   func() time.Time
	// Probes are what only Full Disk Access can read: some folders in the
	// home, and the TCC database.
	Probes []string
	// AppDirs are where apps are installed.
	AppDirs []string
	// Sleep waits between looks at an app starting or quitting.
	Sleep func(time.Duration)
}

// New returns apps' settings for the user whose home is home, with kit's
// state in stateDir and the store's clone in clone, cloned from remote.
func New(run runner.Runner, home, stateDir, clone, remote, mac string, lists Lists, now func() time.Time) *Prefs {
	return &Prefs{
		Run: run, Home: home, State: stateDir, Clone: clone, Remote: remote, Mac: mac, Lists: lists, Now: now,
		// The TCC database last, as kit's tests put theirs elsewhere.
		Probes: []string{
			filepath.Join(home, "Library/Safari"), filepath.Join(home, "Library/Mail"), filepath.Join(home, "Library/Messages"),
			"/Library/Application Support/com.apple.TCC/TCC.db",
		},
		AppDirs: []string{"/Applications", filepath.Join(home, "Applications")},
		Sleep:   time.Sleep,
	}
}

// Load reads the record.
func (p *Prefs) Load() (Record, error) {
	return state.Load[Record](p.State, recordName)
}

// update changes the record.
func (p *Prefs) update(change func(*Record)) error {
	return state.Update(p.State, recordName, change)
}

// StartFresh switches capture on without restoring: the next capture starts
// this Mac's store from what's on it.
func (p *Prefs) StartFresh() error {
	return p.update(func(r *Record) {
		r.On = "started fresh " + p.Now().Format("2006-01-02 15:04")
	})
}

// ClearPending drops the domains waiting to be restored, so capture saves
// what's on the Mac for them: how many there were.
func (p *Prefs) ClearPending() (int, error) {
	n := 0
	err := p.update(func(r *Record) {
		if r.Pending != nil {
			n = len(r.Pending.Domains)
		}
		r.Pending = nil
	})
	return n, err
}

// HasFullDiskAccess reports whether a protected path can be read: without
// Full Disk Access, sandboxed apps' settings export as empty while all
// reports success, and import into the wrong place. Paths missing here
// don't count; with none here at all, access is assumed rather than
// blocked.
func (p *Prefs) HasFullDiskAccess() bool {
	found := false
	for _, probe := range p.Probes {
		info, err := os.Stat(probe)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil {
			if info.IsDir() {
				_, err = os.ReadDir(probe)
			} else {
				var f *os.File
				if f, err = os.Open(probe); err == nil {
					_, err = f.Read(make([]byte, 1))
					_ = f.Close()
				}
			}
		}
		if err == nil {
			return true
		}
		found = true
	}
	return !found
}

// errNoAccess is why capture and restore won't run without Full Disk
// Access.
var errNoAccess = errors.New("no Full Disk Access: grant it to the app or terminal kit runs in (System Settings › Privacy & Security › Full Disk Access)")

// hardwareID is the Mac's hardware UUID, as ioreg reports it.
var hardwareID = regexp.MustCompile(`"IOPlatformUUID" = "([^"]+)"`)

// Hardware is this Mac's hardware UUID.
func (p *Prefs) Hardware(ctx context.Context) (string, error) {
	res, err := p.Run.Run(ctx, runner.Command{Name: "ioreg", Args: []string{"-rd1", "-c", "IOPlatformExpertDevice"}})
	if err != nil {
		return "", fmt.Errorf("read the Mac's hardware id: %w", err)
	}
	m := hardwareID.FindSubmatch(res.Stdout)
	if m == nil {
		return "", errors.New("read the Mac's hardware id: ioreg didn't say it")
	}
	return string(m[1]), nil
}

// Owner is who a Mac's folder in the store belongs to.
type Owner struct {
	Hardware string
	Model    string
	Since    string
}

// ownerFile names, in a Mac's folder, the record of its owner.
const ownerFile = "owner"

// readOwner reads the owner of the folder store: none when it has no record.
func readOwner(store string) (Owner, bool, error) {
	data, err := os.ReadFile(filepath.Join(store, ownerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Owner{}, false, nil
	}
	if err != nil {
		return Owner{}, false, err
	}
	var o Owner
	for line := range strings.Lines(string(data)) {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch k {
		case "hardware":
			o.Hardware = v
		case "model":
			o.Model = v
		case "since":
			o.Since = v
		}
	}
	return o, o.Hardware != "", nil
}

// claim records this Mac as the owner of store.
func (p *Prefs) claim(ctx context.Context, store, hardware string) error {
	model := ""
	if res, err := p.Run.Run(ctx, runner.Command{Name: "sysctl", Args: []string{"-n", "hw.model"}}); err == nil {
		model = strings.TrimSpace(string(res.Stdout))
	}
	text := "hardware " + hardware + "\n"
	if model != "" {
		text += "model " + model + "\n"
	}
	text += "since " + p.Now().Format("2006-01-02 15:04") + "\n"
	return writeIfChanged(filepath.Join(store, ownerFile), []byte(text))
}

// expand is a path with a leading ~/ made the home folder's.
func (p *Prefs) expand(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(p.Home, rest)
	}
	if path == "~" {
		return p.Home
	}
	return path
}

// matches reports whether value matches any of patterns, as Python's
// fnmatch does: * and ? match any character, a slash too; [...] a class,
// [!...] its complement.
func matches(value string, patterns []string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return fnmatch(value, p) })
}

func fnmatch(value, pattern string) bool {
	return translate(pattern).MatchString(value)
}

// translate is a glob pattern as a regular expression matching the whole
// value, as Python's fnmatch.translate makes it.
func translate(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^(?s:")
	rs := []rune(pattern)
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := i + 1
			if j < len(rs) && rs[j] == '!' {
				j++
			}
			if j < len(rs) && rs[j] == ']' {
				j++
			}
			for j < len(rs) && rs[j] != ']' {
				j++
			}
			if j >= len(rs) {
				b.WriteString(`\[`)
				continue
			}
			class := string(rs[i+1 : j])
			class = strings.ReplaceAll(class, `\`, `\\`)
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			} else if strings.HasPrefix(class, "^") {
				class = `\` + class
			}
			b.WriteString("[" + class + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(")$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile(`^` + regexp.QuoteMeta(pattern) + `$`)
	}
	return re
}

// hasMagic reports whether a path is a glob: it holds *, ? or [.
func hasMagic(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

// glob is the paths a pattern matches, sorted, as Python's glob.glob finds
// them: a wildcard never matches a name starting with a dot unless the
// pattern's part starts with one.
func glob(pattern string) []string {
	parts := strings.Split(pattern, "/")
	found := []string{"/"}
	if parts[0] != "" {
		found = []string{parts[0]}
	}
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		var next []string
		for _, dir := range found {
			if !hasMagic(part) {
				if _, err := os.Lstat(filepath.Join(dir, part)); err == nil {
					next = append(next, filepath.Join(dir, part))
				}
				continue
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(part, ".") {
					continue
				}
				if fnmatch(e.Name(), part) {
					next = append(next, filepath.Join(dir, e.Name()))
				}
			}
		}
		found = next
	}
	slices.Sort(found)
	return found
}

// writeIfChanged writes data to path, whole or not at all, unless it holds
// those bytes already.
func writeIfChanged(path string, data []byte) error {
	_, err := write(path, data)
	return err
}

// write writes data to path, whole or not at all, unless it holds those
// bytes already: "new" or "changed" when it wrote, else "".
func write(path string, data []byte) (string, error) {
	old, err := os.ReadFile(path)
	what := "changed"
	switch {
	case err == nil && string(old) == string(data):
		return "", nil
	case errors.Is(err, fs.ErrNotExist):
		what = "new"
	case err != nil:
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return what, nil
}

// plural is n and a word, made plural unless n is 1.
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
