package secret

import "github.com/leeovery/kit/internal/kind/secret/store"

// EnvOf is Env, for stores rather than those kit knows.
func EnvOf(stores []store.Driver, getenv func(string) string, environ []string) []string {
	return drivers(stores).env(getenv, environ)
}
