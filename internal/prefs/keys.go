package prefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// keyedBy are the domains kept by their keys (keyed), by domain.
func (p *Prefs) keyedBy() map[string]Keys {
	by := map[string]Keys{}
	for _, k := range p.keyed() {
		by[k.Domain] = k
	}
	return by
}

// byKeys are domains split into those kept whole and those kept by their
// keys, in order.
func (p *Prefs) byKeys(domains []string) (whole []string, keyed []Keys) {
	by := p.keyedBy()
	for _, d := range domains {
		if k, ok := by[d]; ok {
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
	by := p.keyedBy()
	for _, d := range stored {
		if _, byKeys := by[d]; !byKeys && !matches(d, p.Lists.Deny) && !matches(d, bound) {
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

// LeftOutWhy is why a domain kept by its keys is held once a restore left
// out items pointing at files not on this Mac: the next pass merges it
// again once one of them is here, so a Dock gets the tiles of apps
// installed later.
const LeftOutWhy = "kept till what it left out is on this Mac"

// merging is how domains kept by their keys are restored: from their
// stored copies in dir, the live settings saved first in saved; dryRun,
// only saying they would be. wrote is what kit last wrote of each domain
// held for what it left out (LeftOutWhy), as fingerprint has it.
type merging struct {
	dir, saved string
	dryRun     bool
	wrote      map[string]string
}

// restoreKeys merges each of keys' stored copies into its domain, into
// report, then restarts each process named by those it changed, once. One
// that left items out is held (LeftOutWhy), with what it wrote; one held so
// already is merged again only as again says.
func (p *Prefs) restoreKeys(ctx context.Context, report *RestoreReport, keys []Keys, how merging) {
	restart := map[string]bool{}
	for _, k := range keys {
		wanted, err := readStored(filepath.Join(how.dir, k.Domain+".plist"))
		if err != nil {
			report.Failed[k.Domain] = err.Error()
			continue
		}
		put, left := k.toPut(wanted)
		if wrote, held := how.wrote[k.Domain]; held && !p.again(ctx, report, k.Domain, put, wrote) {
			continue
		}
		if how.dryRun {
			report.Restored = append(report.Restored, k.Domain)
			continue
		}
		changed, err := p.mergeKeys(ctx, k.Domain, put, how.saved)
		if err != nil {
			report.Failed[k.Domain] = err.Error()
			continue
		}
		report.Restored = append(report.Restored, k.Domain)
		report.LeftOut += left
		if left > 0 {
			report.Kept[k.Domain], report.Wrote[k.Domain] = LeftOutWhy, fingerprint(put)
		}
		if changed && k.Restart != "" {
			restart[k.Restart] = true
		}
	}
	for _, process := range slices.Sorted(maps.Keys(restart)) {
		// One not running reads its settings as it starts.
		_, _ = p.Run.Run(ctx, runner.Command{Name: "killall", Args: []string{process}})
	}
}

// again reports whether a domain held for what its restore left out, whose
// keys kit last wrote as wrote, is merged again, put being what it would
// put now: only once something left out is on this Mac, and only while the
// domain's keys are as kit wrote them. Else it stays held, nothing on this
// Mac yet; or, changed since, as when the person's rearranged the Dock, it's
// left as it is, its hold dropped (Rearranged).
func (p *Prefs) again(ctx context.Context, report *RestoreReport, domain string, put map[string]any, wrote string) bool {
	if fingerprint(put) == wrote {
		report.Kept[domain], report.Wrote[domain] = LeftOutWhy, wrote
		return false
	}
	live, err := p.export(ctx, domain)
	if err != nil {
		report.Failed[domain] = "its live settings couldn't be read"
		return false
	}
	if fingerprint(pick(live, slices.Collect(maps.Keys(put)))) != wrote {
		report.Rearranged = append(report.Rearranged, domain)
		return false
	}
	return true
}

// keysReady reports whether a domain held, as held has it, for what its
// restore left out has something left out on this Mac now, for the next
// pass to merge it again.
func (p *Prefs) keysReady(held *Pending) func(domain string) bool {
	keyed := p.keyedBy()
	return func(domain string) bool {
		k, ok := keyed[domain]
		if held == nil || held.Why[domain] != LeftOutWhy || !ok {
			return false
		}
		wanted, err := readStored(filepath.Join(p.Clone, held.From, keysDir, domain+".plist"))
		if err != nil {
			return false
		}
		put, _ := k.toPut(wanted)
		return fingerprint(put) != held.Wrote[domain]
	}
}

// readStored is a stored copy's settings.
func readStored(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("not in the store")
	}
	if err != nil {
		return nil, errors.New("its saved copy couldn't be read")
	}
	v, err := plist.Decode(data)
	wanted, _ := v.(map[string]any)
	if err != nil || len(wanted) == 0 {
		return nil, errors.New("its saved copy is empty")
	}
	return wanted, nil
}

// toPut is what a restore puts of wanted, a stored copy: k's keys, an
// array's items pointing at files not on this Mac left out (onThisMac);
// and how many it left out.
func (k Keys) toPut(wanted map[string]any) (map[string]any, int) {
	put, left := pick(wanted, k.Keys), 0
	for key, value := range put {
		var n int
		put[key], n = onThisMac(value)
		left += n
	}
	return put, left
}

// fingerprint is what m's settings hold, as a restore tells whether a
// domain's are as it wrote them: an array by the files its items point at,
// in order, the rest of each item being the app's to rewrite as it reads
// it; anything else as it is.
func fingerprint(m map[string]any) string {
	h := sha256.New()
	for _, key := range slices.Sorted(maps.Keys(m)) {
		h.Write([]byte(key + "\x00"))
		if items, ok := m[key].([]any); ok {
			for _, item := range items {
				path, _ := fileOf(item)
				h.Write([]byte(path + "\x01"))
			}
		} else {
			h.Write([]byte(plist.XML(m[key])))
		}
		h.Write([]byte("\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// mergeKeys puts put, keys of a domain, into the domain as it stands,
// through cfprefsd, its other keys left as they are: the live settings
// saved first, then read back and compared. It reports whether the domain
// changed.
func (p *Prefs) mergeKeys(ctx context.Context, domain string, put map[string]any, saved string) (bool, error) {
	current, err := p.export(ctx, domain)
	if err != nil {
		return false, errors.New("its live settings couldn't be read, to save them first")
	}
	merged := map[string]any{}
	maps.Copy(merged, current)
	maps.Copy(merged, put)
	if plist.Equal(merged, current) {
		return false, nil
	}
	if err := p.saveLive(domain, current, saved); err != nil {
		return false, err
	}
	// The whole domain imported, its other keys as they were exported, so
	// the merge stands whether import merges or replaces.
	whole, err := plist.Encode(merged)
	if err != nil {
		return false, errors.New("its merged settings couldn't be written")
	}
	if _, err := p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"import", domain, "-"}, Input: string(whole)}); err != nil {
		return false, errors.New("macOS didn't take them")
	}
	back, err := p.export(ctx, domain)
	if err != nil || !maps.EqualFunc(put, pick(back, slices.Collect(maps.Keys(put))), plist.Equal) {
		return false, errors.New("macOS took them, but they read back different")
	}
	return true, nil
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
