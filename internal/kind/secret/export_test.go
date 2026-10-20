package secret

import (
	"testing"

	"github.com/leeovery/kit/internal/kind/secret/store"
)

// UseStores has kit know stores alone, as Stores, till t ends.
func UseStores(t testing.TB, stores ...store.Driver) {
	was := Stores
	Stores = stores
	t.Cleanup(func() { Stores = was })
}
