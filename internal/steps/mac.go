package steps

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// The areas steps belong to, as the at-a-glance view groups them.
const (
	AreaBackups = "Backups"
	AreaMac     = "Mac"
	AreaDrift   = "Drift"
	AreaConfig  = "Config"
	AreaManual  = "Manual"
	AreaChecks  = "Checks"
)

// Problem is the state of a check's item that needs fixing: its name says
// what's wrong, its detail what to do.
const Problem = "problem"

// The checks' thresholds, as bin/health had them.
const (
	diskFreeBelow   = 10   // per cent of the startup disk
	swapAbove       = 16.0 // GB
	fseventsdAbove  = 4.0  // GB
	loadPerCore     = 4.0  // the 15-minute load average, per core
	loadGraceOnBoot = 30 * time.Minute
)

// dataVolume is the startup disk's data volume, where everything that grows
// lives.
const dataVolume = "/System/Volumes/Data"

// problem is a check's result with problems as its items, each an id within
// step, what's wrong and what to do.
func problem(step, summary string, problems ...[3]string) check.Result {
	res := check.Result{State: check.Attention, Summary: summary}
	for _, p := range problems {
		res.Items = append(res.Items, check.Item{ID: step + ":" + p[0], Name: p[1], State: Problem, Detail: p[2]})
	}
	return res
}

func failed(err error) check.Result {
	return check.Result{State: check.Failed, Reason: err.Error()}
}

// output runs a command, returning what it printed.
func output(ctx context.Context, run runner.Runner, name string, args ...string) (string, error) {
	res, err := run.Run(ctx, runner.Command{Name: name, Args: args})
	return string(res.Stdout), err
}

// Disk checks the startup disk isn't nearly full.
func Disk(run runner.Runner) engine.Step {
	return engine.Step{
		Name: "disk", Title: "Disk space", Area: AreaMac,
		Check: func(ctx context.Context) check.Result {
			// The system's df, by path: GNU's, which kit's PATH may put first,
			// prints otherwise.
			out, err := output(ctx, run, "/bin/df", "-P", "-k", dataVolume)
			if err != nil {
				return failed(err)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			fields := strings.Fields(lines[len(lines)-1])
			if len(lines) < 2 || len(fields) < 4 {
				return failed(errors.New("couldn't read df's answer"))
			}
			total, err1 := strconv.ParseFloat(fields[1], 64)
			avail, err2 := strconv.ParseFloat(fields[3], 64)
			if err1 != nil || err2 != nil || total == 0 {
				return failed(errors.New("couldn't read df's answer"))
			}
			free := avail / total * 100
			summary := fmt.Sprintf("%.0f%% free", free)
			if free < diskFreeBelow {
				return problem("disk", summary, [3]string{"low", fmt.Sprintf("only %.0f%% free on the startup disk", free), "clear caches or old data; Time Machine's local snapshots need room too"})
			}
			return check.Result{State: check.OK, Summary: summary, Glance: summary}
		},
	}
}

var swapUsed = regexp.MustCompile(`used = ([\d.]+)M`)

// Memory checks macOS's memory pressure and the swap in use.
func Memory(run runner.Runner) engine.Step {
	return engine.Step{
		Name: "memory", Title: "Memory", Area: AreaMac,
		Check: func(ctx context.Context) check.Result {
			out, err := output(ctx, run, "sysctl", "-n", "kern.memorystatus_vm_pressure_level", "vm.swapusage")
			if err != nil {
				return failed(err)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			m := swapUsed.FindStringSubmatch(out)
			if len(lines) < 2 || m == nil {
				return failed(errors.New("couldn't read sysctl's answer"))
			}
			mb, _ := strconv.ParseFloat(m[1], 64)
			swap := mb / 1024
			critical := strings.TrimSpace(lines[0]) == "4"
			pressure := "normal"
			if critical {
				pressure = "critical"
			}
			summary := fmt.Sprintf("%.1f GB swap, pressure %s", swap, pressure)
			var problems [][3]string
			if critical {
				problems = append(problems, [3]string{"critical", "macOS reports critical memory pressure", "Activity Monitor › Memory: quit what's using most; look for runaway agents"})
			}
			if swap > swapAbove {
				problems = append(problems, [3]string{"swap", fmt.Sprintf("%.0f GB of swap in use", swap), "Activity Monitor › Memory: find what's grown (on 27 Sep 2026 it was fseventsd)"})
			}
			if len(problems) > 0 {
				return problem("memory", summary, problems...)
			}
			return check.Result{State: check.OK, Summary: summary, Glance: fmt.Sprintf("%.1f GB swap", swap)}
		},
	}
}

var topMem = regexp.MustCompile(`(?m)^\s*([\d.]+)([BKMGT])\+?\s*$`)

// FileEvents checks fseventsd, the file-events daemon, hasn't bloated: its
// footprint, compressed memory and all, as top counts it.
func FileEvents(run runner.Runner) engine.Step {
	return engine.Step{
		Name: "file-events", Title: "File events", Area: AreaMac,
		Check: func(ctx context.Context) check.Result {
			out, err := output(ctx, run, "pgrep", "-x", "fseventsd")
			if _, exited := errors.AsType[*runner.ExitError](err); exited {
				return check.Result{State: check.OK, Summary: "fseventsd isn't running"}
			}
			if err != nil {
				return failed(err)
			}
			pid := strings.Fields(out)
			if len(pid) == 0 {
				return check.Result{State: check.OK, Summary: "fseventsd isn't running"}
			}
			out, err = output(ctx, run, "top", "-l", "1", "-pid", pid[0], "-stats", "mem")
			if err != nil {
				return failed(err)
			}
			sizes := topMem.FindAllStringSubmatch(out, -1)
			if len(sizes) == 0 {
				return failed(errors.New("couldn't read top's answer"))
			}
			last := sizes[len(sizes)-1]
			value, _ := strconv.ParseFloat(last[1], 64)
			gb := value * map[string]float64{"B": 1.0 / (1 << 30), "K": 1.0 / (1 << 20), "M": 1.0 / 1024, "G": 1, "T": 1024}[last[2]]
			summary := fmt.Sprintf("fseventsd using %.1f GB", gb)
			if gb > fseventsdAbove {
				return problem("file-events", summary, [3]string{"fseventsd", fmt.Sprintf("fseventsd is using %.0f GB, as before the 27 Sep 2026 slowdown", gb), "sudo killall fseventsd (it restarts); then find what's flooding file events"})
			}
			return check.Result{State: check.OK, Summary: summary}
		},
	}
}

var bootSec = regexp.MustCompile(`sec = (\d+)`)

// Load checks the Mac isn't overloaded: the 15-minute load average against
// its cores, except in the half hour after it starts, while everything that
// starts at login catches up.
func Load(run runner.Runner, now func() time.Time) engine.Step {
	return engine.Step{
		Name: "load", Title: "Load", Area: AreaMac,
		Check: func(ctx context.Context) check.Result {
			out, err := output(ctx, run, "sysctl", "-n", "vm.loadavg", "hw.ncpu", "kern.boottime")
			if err != nil {
				return failed(err)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(lines) < 3 {
				return failed(errors.New("couldn't read sysctl's answer"))
			}
			loads := strings.Fields(strings.Trim(strings.TrimSpace(lines[0]), "{}"))
			cores, err := strconv.Atoi(strings.TrimSpace(lines[1]))
			m := bootSec.FindStringSubmatch(lines[2])
			if len(loads) < 3 || err != nil || cores < 1 || m == nil {
				return failed(errors.New("couldn't read sysctl's answer"))
			}
			load, err := strconv.ParseFloat(loads[2], 64)
			if err != nil {
				return failed(errors.New("couldn't read sysctl's answer"))
			}
			sec, _ := strconv.ParseInt(m[1], 10, 64)
			up := now().Sub(time.Unix(sec, 0))
			summary := fmt.Sprintf("load %.1f on %d cores", load, cores)
			switch {
			case load <= loadPerCore*float64(cores):
			case up < loadGraceOnBoot:
				summary += ", just after starting up"
			default:
				return problem("load", summary, [3]string{"high", fmt.Sprintf("load average %.0f over 15 minutes on %d cores", load, cores), "Activity Monitor › CPU: look for runaway processes or orphaned agents"})
			}
			return check.Result{State: check.OK, Summary: summary, Glance: fmt.Sprintf("load %.1f", load)}
		},
	}
}
