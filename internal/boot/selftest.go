package boot

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner"
)

// Space reports a disk's free space and its size, in bytes, for the volume
// path is on.
func Space(path string) (free, size uint64, err error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, err
	}
	return fs.Bavail * uint64(fs.Bsize), fs.Blocks * uint64(fs.Bsize), nil
}

// SelfTest is what the boot finds as it starts: the chip, the memory, the
// disk, macOS and the network, the one thing the boot can't go on without.
// space reports the disk's.
func (b *Boot) SelfTest(ctx context.Context, space func(string) (uint64, uint64, error)) event.SelfTest {
	out := func(args ...string) string {
		res, err := b.Run.Run(ctx, runner.Command{Name: args[0], Args: args[1:], Timeout: 10 * time.Second})
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(res.Stdout))
	}
	test := event.SelfTest{Time: b.Now(), Mac: out("scutil", "--get", "LocalHostName")}
	add := func(name string, state check.State, says ...string) {
		test.Tests = append(test.Tests, event.Test{Name: name, State: state, Says: says})
	}

	chip := out("sysctl", "-n", "machdep.cpu.brand_string")
	add(cmp.Or(chip, "Chip"), check.OK, cores(out("sysctl", "-n", "hw.ncpu")))
	if mem, err := strconv.ParseUint(out("sysctl", "-n", "hw.memsize"), 10, 64); err == nil {
		add("Memory", check.OK, fmt.Sprintf("%d MB", mem>>20))
	}
	if free, size, err := space("/"); err == nil {
		says := []string{fmt.Sprintf("%d GB free of %d GB", free/1e9, size/1e9)}
		if name := volumeName(out("diskutil", "info", "/")); name != "" {
			says = append([]string{name}, says...)
		}
		add("Disk", check.OK, says...)
	}
	add("macOS", check.OK, out("sw_vers", "-productVersion"))
	if took, err := b.reach(ctx); err != nil {
		add("Network", check.Failed, "no answer from github.com", err.Error())
	} else {
		add("Network", check.OK, "github.com", fmt.Sprintf("%d ms", took.Milliseconds()))
	}

	return test
}

// reach times an HTTPS request to GitHub, as the network's check.
func (b *Boot) reach(ctx context.Context) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, b.GitHub.Site, nil)
	if err != nil {
		return 0, err
	}
	start := b.Now()
	res, err := b.GitHub.Client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = res.Body.Close()
	return b.Now().Sub(start), nil
}

// cores says how many cores ncpu counts.
func cores(ncpu string) string {
	if ncpu == "" {
		return ""
	}
	return ncpu + " cores"
}

// volumeName is the name diskutil's info gives a volume.
func volumeName(info string) string {
	for l := range strings.SplitSeq(info, "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "Volume Name" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
