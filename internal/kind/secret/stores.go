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
// its store, so a config can use several. It's the one list the config's
// secrets sections and the secrets kind read.
var Stores = []store.Driver{
	onepassword.Driver{},
}

// drivers are secrets stores: Stores, as a kind reads them.
type drivers []store.Driver

// stores are the stores kit knows, as Stores has them now.
func stores() drivers { return drivers(Stores) }

// forRef is the store whose value ref is, in full.
func (ds drivers) forRef(ref string) (store.Driver, bool) {
	i := slices.IndexFunc(ds, func(d store.Driver) bool { return d.IsRef(ref) })
	if i < 0 {
		return nil, false
	}
	return ds[i], true
}

// forItem is the store whose item ref is.
func (ds drivers) forItem(ref string) (store.Driver, bool) {
	i := slices.IndexFunc(ds, func(d store.Driver) bool { return d.IsItem(ref) })
	if i < 0 {
		return nil, false
	}
	return ds[i], true
}

// owns reports whether ref is one of a store's own references, a value's
// in full or an item's: never short for a field of an item.
func (ds drivers) owns(ref string) bool {
	_, value := ds.forRef(ref)
	_, item := ds.forItem(ref)
	return value || item
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

// checkItem says why ref isn't one of a store's items, as what follows
// [secrets in an item's section's header: nil when it is.
func checkItem(ref string) error {
	if _, ok := stores().forItem(ref); ok {
		return nil
	}
	return fmt.Errorf("%q isn't an item's reference kit knows: %s", ref, stores().items())
}

// ItemStore is the store whose item ref is: an error, naming the items kit
// knows, when it's no store's.
func ItemStore(ref string) (store.Driver, error) {
	if d, ok := stores().forItem(ref); ok {
		return d, nil
	}
	return nil, checkItem(ref)
}

// ValueStore is the store whose value ref is, in full: an error, naming the
// references kit knows, when it's no store's.
func ValueStore(ref string) (store.Driver, error) {
	if d, ok := stores().forRef(ref); ok {
		return d, nil
	}
	return nil, fmt.Errorf("%q isn't where a store keeps a value, in full: %s", ref, stores().refs())
}

// Items says how the stores' items' references read, as in 1Password's,
// op://vault/item.
func Items() string { return stores().items() }

// Env are the variables of environ, kit's environment, that the stores'
// programs need passed on to what kit runs, as each store names them
// (store.About.Env), each as name=value: a name's value as getenv has it,
// and every variable a name ending * starts.
func Env(getenv func(string) string, environ []string) []string {
	var env []string
	for _, d := range Stores {
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
