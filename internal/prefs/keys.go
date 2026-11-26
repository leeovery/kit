package prefs

import (
	"cmp"
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
// into report as captureDomain does: the domains whose stored copies stay;
// and, of those held for what their restores left out (held), those the
// person's changed since, whose holds go (released), captured as any. One
// held so as kit wrote it, or waiting otherwise, is left as stored when
// protected.
func (p *Prefs) captureKeys(ctx context.Context, report *Report, dir string, protected map[string]bool, held map[string]KeysHeld) (keep map[string]bool, released []string) {
	keep = map[string]bool{}
	for _, k := range p.keyed() {
		kh, isHeld := held[k.Domain]
		if protected[k.Domain] && !isHeld {
			keep[k.Domain] = true
			continue
		}
		m, err := p.export(ctx, k.Domain)
		if err == nil && isHeld {
			if k.fingerprint(m) != kh.Wrote {
				released = append(released, k.Domain)
			} else if protected[k.Domain] {
				keep[k.Domain] = true
				continue
			}
		}
		var content []byte
		if err == nil {
			content, err = k.picked(m)
		}
		if p.captureDomain(report, filepath.Join(dir, k.Domain+".plist"), k.Domain, content, err) {
			keep[k.Domain] = true
		}
	}
	return keep, released
}

// LeftOutWhy is why a domain kept by its keys is held once a restore left
// out items pointing at files not on this Mac (KeysHeld): the next pass
// puts each back once its file is here, so a Dock gets the tiles of apps
// installed later.
const LeftOutWhy = "waiting for apps to put its items back"

// KeysHeld is a domain kept by its keys held for what its restore left
// out: those items, and what kit wrote of its keys, as fingerprint has it,
// so a domain the person's changed since is never written over.
type KeysHeld struct {
	Wrote   string    `json:"wrote"`
	LeftOut []LeftOut `json:"left_out"`
}

// LeftOut is an item a restore left out, pointing at a file not on this
// Mac: its key, where it was in the key's array, the file, and the item, as
// an XML property list's element.
type LeftOut struct {
	Key   string `json:"key"`
	Index int    `json:"index"`
	Path  string `json:"path"`
	Item  string `json:"item"`
}

// merging is how domains kept by their keys are restored: from their
// stored copies in dir, the live settings saved first in saved; dryRun,
// only saying they would be. held are those held for what their restores
// left out, which only have that put back.
type merging struct {
	dir, saved string
	dryRun     bool
	held       map[string]KeysHeld
}

// restoreKeys restores each of keys into its domain, into report, then
// restarts each process named by those it changed, once: from its stored
// copy (restoreStored), or, held for what its restore left out, only that
// (putBack).
func (p *Prefs) restoreKeys(ctx context.Context, report *RestoreReport, keys []Keys, how merging) {
	restart := map[string]bool{}
	for _, k := range keys {
		var changed bool
		var err error
		if kh, isHeld := how.held[k.Domain]; isHeld {
			changed, err = p.putBack(ctx, report, k, kh, how)
		} else {
			changed, err = p.restoreStored(ctx, report, k, how)
		}
		if err != nil {
			report.Failed[k.Domain] = err.Error()
			continue
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

// restoreStored merges k's keys from their stored copy into the domain, an
// array's items pointing at files not on this Mac left out (onThisMac),
// the domain held for them: whether it changed the domain.
func (p *Prefs) restoreStored(ctx context.Context, report *RestoreReport, k Keys, how merging) (bool, error) {
	wanted, err := readStored(filepath.Join(how.dir, k.Domain+".plist"))
	if err != nil {
		return false, err
	}
	put, out := k.toPut(wanted)
	if how.dryRun {
		report.Restored = append(report.Restored, k.Domain)
		report.keep(k.Domain, KeysHeld{LeftOut: out})
		return false, nil
	}
	changed, after, err := p.mergeKeys(ctx, k.Domain, put, how.saved)
	if err != nil {
		return false, err
	}
	report.Restored = append(report.Restored, k.Domain)
	report.keep(k.Domain, KeysHeld{Wrote: k.fingerprint(after), LeftOut: out})
	return changed, nil
}

// putBack puts the items a domain's restore left out, kh, whose files are
// on this Mac now, back where they were, and nothing else, whatever the
// store holds now: only while the domain's keys are as kit wrote them;
// changed since, as a Dock the person's rearranged, it's left as it is,
// held no more (Rearranged). What's still not here stays held. It reports
// whether it changed the domain.
func (p *Prefs) putBack(ctx context.Context, report *RestoreReport, k Keys, kh KeysHeld, how merging) (bool, error) {
	live, err := p.export(ctx, k.Domain)
	if err != nil {
		return false, errors.New("its live settings couldn't be read")
	}
	if k.fingerprint(live) != kh.Wrote {
		report.Rearranged = append(report.Rearranged, k.Domain)
		return false, nil
	}
	put, still := map[string]any{}, []LeftOut{}
	for key, out := range byKey(kh.LeftOut) {
		items, _ := live[key].([]any)
		back, left := restoreItems(items, out)
		if len(left) < len(out) {
			put[key] = back
		}
		still = append(still, left...)
	}
	slices.SortFunc(still, func(a, b LeftOut) int { return cmp.Or(cmp.Compare(a.Key, b.Key), cmp.Compare(a.Index, b.Index)) })
	if len(put) == 0 {
		report.keep(k.Domain, kh)
		return false, nil
	}
	if how.dryRun {
		report.Restored = append(report.Restored, k.Domain)
		report.keep(k.Domain, KeysHeld{Wrote: kh.Wrote, LeftOut: still})
		return false, nil
	}
	changed, after, err := p.mergeKeys(ctx, k.Domain, put, how.saved)
	if err != nil {
		return false, err
	}
	report.Restored = append(report.Restored, k.Domain)
	report.keep(k.Domain, KeysHeld{Wrote: k.fingerprint(after), LeftOut: still})
	return changed, nil
}

// keep holds domain, kept by its keys, for what it left out, kh: nothing,
// with nothing left out.
func (r *RestoreReport) keep(domain string, kh KeysHeld) {
	if len(kh.LeftOut) > 0 {
		r.Kept[domain], r.Held[domain] = LeftOutWhy, kh
	}
}

// byKey are items left out, by their keys.
func byKey(out []LeftOut) map[string][]LeftOut {
	by := map[string][]LeftOut{}
	for _, o := range out {
		by[o.Key] = append(by[o.Key], o)
	}
	return by
}

// restoreItems is items, a key's array as kit wrote it, with those of out,
// left out of it, whose files are here now put back where they were, in
// order; and those still not here. An item's place is its place in the
// array as stored less those before it still left out.
func restoreItems(items []any, out []LeftOut) ([]any, []LeftOut) {
	items = slices.Clone(items)
	slices.SortFunc(out, func(a, b LeftOut) int { return cmp.Compare(a.Index, b.Index) })
	var still []LeftOut
	for _, o := range out {
		item, err := plist.ReadValue(o.Item)
		if err != nil || !here(o.Path) {
			still = append(still, o)
			continue
		}
		items = slices.Insert(items, min(o.Index-len(still), len(items)), item)
	}
	return items, still
}

// keysReady reports whether a domain held, as held has it, for what its
// restore left out has a file of what it left out on this Mac now, for the
// next pass to put back.
func keysReady(held *Pending) func(domain string) bool {
	return func(domain string) bool {
		if held == nil || held.Why[domain] != LeftOutWhy {
			return false
		}
		return slices.ContainsFunc(held.Keyed[domain].LeftOut, func(o LeftOut) bool { return here(o.Path) })
	}
}

// here reports whether there's a file at path.
func here(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
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
// and those it left out.
func (k Keys) toPut(wanted map[string]any) (map[string]any, []LeftOut) {
	put := pick(wanted, k.Keys)
	var out []LeftOut
	for _, key := range slices.Sorted(maps.Keys(put)) {
		var left []LeftOut
		put[key], left = onThisMac(key, put[key])
		out = append(out, left...)
	}
	return put, out
}

// fingerprint is what m holds of k's keys, as a restore tells whether a
// domain's are as it wrote them: an array by the files its items point at,
// in order, the rest of each item being the app's to rewrite as it reads
// it; anything else as it is.
func (k Keys) fingerprint(m map[string]any) string {
	m = pick(m, k.Keys)
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
// changed, and the domain as it stands after.
func (p *Prefs) mergeKeys(ctx context.Context, domain string, put map[string]any, saved string) (bool, map[string]any, error) {
	current, err := p.export(ctx, domain)
	if err != nil {
		return false, nil, errors.New("its live settings couldn't be read, to save them first")
	}
	merged := map[string]any{}
	maps.Copy(merged, current)
	maps.Copy(merged, put)
	if plist.Equal(merged, current) {
		return false, current, nil
	}
	if err := p.saveLive(domain, current, saved); err != nil {
		return false, nil, err
	}
	// The whole domain imported, its other keys as they were exported, so
	// the merge stands whether import merges or replaces.
	whole, err := plist.Encode(merged)
	if err != nil {
		return false, nil, errors.New("its merged settings couldn't be written")
	}
	if _, err := p.Run.Run(ctx, runner.Command{Name: "defaults", Args: []string{"import", domain, "-"}, Input: string(whole)}); err != nil {
		return false, nil, errors.New("macOS didn't take them")
	}
	back, err := p.export(ctx, domain)
	if err != nil || !maps.EqualFunc(put, pick(back, slices.Collect(maps.Keys(put))), plist.Equal) {
		return false, nil, errors.New("macOS took them, but they read back different")
	}
	return true, back, nil
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

// onThisMac is value, key's, an array's items pointing at a file not on
// this Mac left out, as the Dock's tiles point at their apps and folders:
// an item holding a file's URL as CoreFoundation saves one (_CFURLString);
// and those it left out. Any other value is as it is.
func onThisMac(key string, value any) (any, []LeftOut) {
	items, ok := value.([]any)
	if !ok {
		return value, nil
	}
	kept := []any{}
	var out []LeftOut
	for i, item := range items {
		if path, ok := fileOf(item); ok && !here(path) {
			out = append(out, LeftOut{Key: key, Index: i, Path: path, Item: plist.XML(item)})
			continue
		}
		kept = append(kept, item)
	}
	return kept, out
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
