package prefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// Keys are keys of one preferences domain that prefs keeps, the rest of
// the domain left out, as a [prefs keys] line declares them: a domain
// prefs otherwise skips, as Apple's are, or any other; and the process
// restarted once they're put back, so it reads them, if any.
type Keys struct {
	Domain  string
	Keys    []string
	Restart string
}

// keysDir names, in a Mac's folder, the folder of the domains kept by
// their keys: a restore merges each into its domain, never replaces the
// domain with it, as it does a domain kept whole.
const keysDir = "keys"

// restartOption names the process a [prefs keys] line restarts.
const restartOption = "--restart"

// ParseKeys reads a [prefs keys] line: its domain, then the keys kept,
// then --restart and a process's name, if any. A line that doesn't read
// so is refused, saying where.
func ParseKeys(e config.Entry) (Keys, error) {
	k, err := readKeys(e.Name, e.Value)
	if err != nil {
		return k, fmt.Errorf("%s: %w", e.Pos(), err)
	}
	return k, nil
}

func readKeys(domain, options string) (Keys, error) {
	k := Keys{Domain: domain}
	words, err := config.Words(options)
	if err != nil {
		return k, err
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == restartOption && k.Restart != "":
			return k, fmt.Errorf("%s is there twice: a line restarts one process", restartOption)
		case w == restartOption && (i+1 == len(words) || strings.HasPrefix(words[i+1], "-")):
			return k, fmt.Errorf("%s needs a process's name after it, as in %s Dock", restartOption, restartOption)
		case w == restartOption:
			i++
			k.Restart = words[i]
		case strings.HasPrefix(w, "-"):
			return k, fmt.Errorf("%q isn't one of [prefs keys]'s options: %s", w, restartOption)
		case slices.Contains(k.Keys, w):
			return k, fmt.Errorf("%s is named twice", w)
		default:
			k.Keys = append(k.Keys, w)
		}
	}
	if len(k.Keys) == 0 {
		return k, fmt.Errorf("%s names no keys: the keys kept follow the domain, as in com.apple.dock persistent-apps", domain)
	}
	return k, nil
}

// keyed are the domains kept by their keys, as kit-config declares them,
// but those [prefs deny] leaves out.
func (p *Prefs) keyed() []Keys {
	return slices.DeleteFunc(slices.Clone(p.Lists.Keys), func(k Keys) bool { return matches(k.Domain, p.Lists.Deny) })
}

// keysOf is the domain's declared keys, if it's kept by its keys.
func (p *Prefs) keysOf(domain string) (Keys, bool) {
	keyed := p.keyed()
	i := slices.IndexFunc(keyed, func(k Keys) bool { return k.Domain == domain })
	if i < 0 {
		return Keys{}, false
	}
	return keyed[i], true
}

// byKeys are domains split into those kept whole and those kept by their
// keys, in order.
func (p *Prefs) byKeys(domains []string) (whole []string, keyed []Keys) {
	for _, d := range domains {
		if k, ok := p.keysOf(d); ok {
			keyed = append(keyed, k)
		} else {
			whole = append(whole, d)
		}
	}
	return whole, keyed
}

// allStored are the domains a full restore restores, of those stored whole
// and those stored by their keys: each but those [prefs deny] leaves out
// and those bound to one Mac (bound); of those stored whole, none kept by
// its keys now, whose whole copy, saved before, a restore never puts back.
func (p *Prefs) allStored(stored, storedKeyed, bound []string) (whole []string, keyed []Keys) {
	for _, d := range stored {
		if _, byKeys := p.keysOf(d); !byKeys && !matches(d, p.Lists.Deny) && !matches(d, bound) {
			whole = append(whole, d)
		}
	}
	for _, k := range p.keyed() {
		if slices.Contains(storedKeyed, k.Domain) && !matches(k.Domain, bound) {
			keyed = append(keyed, k)
		}
	}
	return whole, keyed
}

// notStored are those of whole and keyed, domains named, with no stored
// copy of their own kind, of those stored whole and by their keys.
func notStored(whole []string, keyed []Keys, stored, storedKeyed []string) []string {
	var missing []string
	for _, d := range whole {
		if !slices.Contains(stored, d) {
			missing = append(missing, d)
		}
	}
	for _, k := range keyed {
		if !slices.Contains(storedKeyed, k.Domain) {
			missing = append(missing, k.Domain)
		}
	}
	return missing
}

// picked is the settings of m that k keeps, as the store keeps them: sorted
// XML; nil when m holds none of them, so the domain counts as gone.
func (k Keys) picked(m map[string]any) ([]byte, error) {
	out := pick(m, k.Keys)
	if len(out) == 0 {
		return nil, nil
	}
	return plist.Encode(out)
}

// captureKeys saves the keys each domain kept by its keys has into dir,
// into report as captureDomain does, but those protected, waiting to be
// restored, left as stored: the domains whose stored copies stay.
func (p *Prefs) captureKeys(ctx context.Context, report *Report, dir string, protected map[string]bool) map[string]bool {
	keep := map[string]bool{}
	for _, k := range p.keyed() {
		if protected[k.Domain] {
			keep[k.Domain] = true
			continue
		}
		m, err := p.export(ctx, k.Domain)
		var content []byte
		if err == nil {
			content, err = k.picked(m)
		}
		if p.captureDomain(report, filepath.Join(dir, k.Domain+".plist"), k.Domain, content, err) {
			keep[k.Domain] = true
		}
	}
	return keep
}

// restoreKeys merges each of keys' stored copies, in dir, into its domain,
// into report, then restarts each process named by those it changed, once.
// A domain with no stored copy fails; dryRun only says what it would
// restore.
func (p *Prefs) restoreKeys(ctx context.Context, report *RestoreReport, keys []Keys, dir, saved string, dryRun bool) {
	restart := map[string]bool{}
	for _, k := range keys {
		stored := filepath.Join(dir, k.Domain+".plist")
		if _, err := os.Stat(stored); err != nil {
			report.Failed[k.Domain] = "not in the store"
			continue
		}
		if dryRun {
			report.Restored = append(report.Restored, k.Domain)
			continue
		}
		changed, left, err := p.mergeKeys(ctx, k, stored, saved)
		if err != nil {
			report.Failed[k.Domain] = err.Error()
			continue
		}
		report.Restored = append(report.Restored, k.Domain)
		report.LeftOut += left
		if changed && k.Restart != "" {
			restart[k.Restart] = true
		}
	}
	for _, process := range slices.Sorted(maps.Keys(restart)) {
		// One not running reads its settings as it starts.
		_, _ = p.Run.Run(ctx, runner.Command{Name: "killall", Args: []string{process}})
	}
}

// mergeKeys puts k's keys from their stored copy into the domain as it
// stands, through cfprefsd, its other keys left as they are: the stored
// copy checked first, the live settings saved, then read back and
// compared. An array's items pointing at a file not on this Mac are left
// out (onThisMac). It reports whether the domain changed, and how many
// items it left out.
func (p *Prefs) mergeKeys(ctx context.Context, k Keys, stored, saved string) (changed bool, left int, err error) {
	data, err := os.ReadFile(stored)
	if err != nil {
		return false, 0, errors.New("its saved copy couldn't be read")
	}
	v, err := plist.Decode(data)
	wanted, _ := v.(map[string]any)
	if err != nil || len(wanted) == 0 {
		return false, 0, errors.New("its saved copy is empty")
	}
	current, err := p.export(ctx, k.Domain)
	if err != nil {
		return false, 0, errors.New("its live settings couldn't be read, to save them first")
	}
	merged, put := map[string]any{}, map[string]any{}
	maps.Copy(merged, current)
	for _, key := range k.Keys {
		value, ok := wanted[key]
		if !ok {
			continue
		}
		value, n := onThisMac(value)
		left += n
		merged[key], put[key] = value, value
	}
	if plist.Equal(merged, current) {
		return false, left, nil
	}
	if err := p.saveLive(k.Domain, current, saved); err != nil {
		return false, left, err
	}
	// The whole domain imported, its other keys as they were exported, so
	// the merge stands whether import merges or replaces.
	whole, err := plist.Encode(merged)
	if err != nil {
		return false, left, errors.New("its merged settings couldn't be written")
	}
	if _, err := p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"import", k.Domain, "-"}, Input: string(whole)}); err != nil {
		return false, left, errors.New("macOS didn't take them")
	}
	back, err := p.export(ctx, k.Domain)
	if err != nil || !maps.EqualFunc(put, pick(back, slices.Collect(maps.Keys(put))), plist.Equal) {
		return false, left, errors.New("macOS took them, but they read back different")
	}
	return true, left, nil
}

// pick is the settings of m under keys, those it has.
func pick(m map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			out[key] = v
		}
	}
	return out
}

// onThisMac is value, an array's items pointing at a file not on this Mac
// left out, as the Dock's tiles point at their apps and folders: an item
// holding a file's URL as CoreFoundation saves one (_CFURLString); and how
// many it left out. Any other value is as it is.
func onThisMac(value any) (any, int) {
	items, ok := value.([]any)
	if !ok {
		return value, 0
	}
	kept := slices.DeleteFunc(slices.Clone(items), func(item any) bool {
		path, ok := fileOf(item)
		if !ok {
			return false
		}
		_, err := os.Stat(path)
		return errors.Is(err, fs.ErrNotExist)
	})
	return kept, len(items) - len(kept)
}

// fileOf is the path of the file an item points at, the first URL to one
// found in it, a dict's own before those of the dicts in it: a file: URL,
// or a path, as CoreFoundation saves a URL of each type.
func fileOf(item any) (string, bool) {
	m, ok := item.(map[string]any)
	if !ok {
		return "", false
	}
	if s, ok := m["_CFURLString"].(string); ok {
		if strings.HasPrefix(s, "/") {
			return s, true
		}
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			return "", false
		}
		return u.Path, true
	}
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if path, ok := fileOf(m[key]); ok {
			return path, true
		}
	}
	return "", false
}
