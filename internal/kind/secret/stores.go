package secret

import (
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/kind/secret/onepassword"
	"github.com/leeovery/kit/internal/kind/secret/store"
)

// Stores are the secrets stores kit knows, each a driver in a folder of its
// own: a store comes and goes with its line here. A secret's reference picks
// its store, so a config can use several.
var Stores = []store.Driver{
	onepassword.Driver{},
}

// drivers are secrets stores: those a kind is made with.
type drivers []store.Driver

// ofRef is the store whose value ref is, in full.
func (ds drivers) ofRef(ref string) (store.Driver, bool) {
	i := slices.IndexFunc(ds, func(d store.Driver) bool { return d.IsRef(ref) })
	if i < 0 {
		return nil, false
	}
	return ds[i], true
}

// ofItem is the store whose item ref is.
func (ds drivers) ofItem(ref string) (store.Driver, bool) {
	i := slices.IndexFunc(ds, func(d store.Driver) bool { return d.IsItem(ref) })
	if i < 0 {
		return nil, false
	}
	return ds[i], true
}

// refs says how the stores' values' references read, in full: each store's,
// as in 1Password's, op://vault/item/field.
func (ds drivers) refs() string {
	return ds.say(func(a store.About) string { return a.Ref })
}

// items says how the stores' items' references read: each store's, as in
// 1Password's, op://vault/item.
func (ds drivers) items() string {
	return ds.say(func(a store.About) string { return a.Item })
}

// say is each store's example, as example has it, by the store's name.
func (ds drivers) say(example func(store.About) string) string {
	said := make([]string, len(ds))
	for i, d := range ds {
		a := d.About()
		said[i] = fmt.Sprintf("%s's, as in %s", a.Name, example(a))
	}
	return strings.Join(said, "; ")
}

// itemOf is nil when ref is one of a store's items, else why not: what
// follows [secrets in an item's section's header.
func (ds drivers) itemOf(ref string) error {
	if _, ok := ds.ofItem(ref); ok {
		return nil
	}
	return fmt.Errorf("%q isn't an item's reference kit knows: %s", ref, ds.items())
}

// Env are the variables of environ, kit's environment, that the stores'
// programs need passed on to what kit runs, as each store names them
// (store.About.Env), each as name=value: a name's value as getenv has it,
// and every variable a name ending * starts.
func Env(getenv func(string) string, environ []string) []string {
	return drivers(Stores).env(getenv, environ)
}

// env are the variables of environ the stores' programs need passed on, as
// Env says.
func (ds drivers) env(getenv func(string) string, environ []string) []string {
	var env []string
	for _, d := range ds {
		for _, name := range d.About().Env {
			if prefix, ok := strings.CutSuffix(name, "*"); ok {
				for _, kv := range environ {
					if strings.HasPrefix(kv, prefix) {
						env = append(env, kv)
					}
				}
			} else if v := getenv(name); v != "" {
				env = append(env, name+"="+v)
			}
		}
	}
	return env
}
