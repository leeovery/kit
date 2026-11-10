package oldmac

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// sshService is Remote Login, as Bonjour offers it.
var sshService = []string{"_ssh._tcp", "local."}

// loopback is the interface Bonjour sees this Mac's own services on, lo0's
// index: macOS numbers it 1. A Mac offering Remote Login itself sees its own
// name there, as well as on its network's interfaces, where no other Mac is.
const loopback = 1

var (
	// browsed is a line of dns-sd -B: when, Add or Rmv, its flags, the
	// interface, the domain, the service and the instance's name, which can
	// hold spaces.
	browsed = regexp.MustCompile(`^\S+\s+(Add|Rmv)\s+\d+\s+(\d+)\s+\S+\s+\S+\s+(.+?)\s*$`)
	// reached is dns-sd -L's answer: the host the instance is on, its name
	// ending in a dot, and the port.
	reached = regexp.MustCompile(`can be reached at (\S+?)\.?:(\d+) `)
)

// browse is the names of the Macs Bonjour finds offering Remote Login in
// so long, but this Mac, each once, sorted: one withdrawn from every
// interface it was offered on (Rmv) is gone.
func (f Finder) browse(ctx context.Context) ([]string, error) {
	on := map[string][]string{}
	own := map[string]bool{}
	lines := func(line string) bool {
		m := browsed.FindStringSubmatch(line)
		if m == nil {
			return false
		}
		added, iface, name := m[1] == "Add", m[2], m[3]
		if added {
			on[name] = append(on[name], iface)
			own[name] = own[name] || iface == strconv.Itoa(loopback)
		} else if i := slices.Index(on[name], iface); i >= 0 {
			on[name] = slices.Delete(on[name], i, i+1)
		}
		return false
	}
	args := slices.Concat([]string{"-B"}, sshService)
	if err := f.listen(ctx, cmp.Or(f.Browse, browseFor), args, lines); err != nil {
		return nil, fmt.Errorf("look for Macs offering Remote Login: %w", err)
	}
	var names []string
	for name, ifaces := range on {
		if len(ifaces) > 0 && !own[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// address is where a Mac offering Remote Login under a name answers.
type address struct {
	host string
	port int
}

// resolve is where the Mac offering Remote Login as name answers: ok false
// when it says nothing in so long.
func (f Finder) resolve(ctx context.Context, name string) (a address, ok bool, err error) {
	lines := func(line string) bool {
		m := reached.FindStringSubmatch(line)
		if m == nil {
			return false
		}
		a.host, ok = m[1], true
		a.port, _ = strconv.Atoi(m[2])
		return true
	}
	args := slices.Concat([]string{"-L", name}, sshService)
	if err := f.listen(ctx, cmp.Or(f.Resolve, resolveFor), args, lines); err != nil {
		return address{}, false, fmt.Errorf("find where %s answers: %w", name, err)
	}
	return a, ok, nil
}

// listen runs dns-sd with args, handing lines each line it prints, one at a
// time, till wait's up or lines says it's heard what it listens for:
// neither is an error, as dns-sd never ends by itself.
func (f Finder) listen(ctx context.Context, wait time.Duration, args []string, lines func(line string) (done bool)) error {
	listening, stop := context.WithTimeout(ctx, wait)
	defer stop()
	var mu sync.Mutex
	heard := false
	each := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		if !heard && lines(line) {
			heard = true
			stop()
		}
	}
	_, err := f.Run.Run(listening, runner.Command{Name: "dns-sd", Args: args, Lines: each, Timeout: runner.Unlimited})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && listening.Err() == nil {
		return err
	}
	return nil
}
