package devtool

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/kind"
)

// npmLine is how kit npm takes a line copied for npm: kit npm install -g
// typescript. npm's add, remove and rm go to kit's own commands of those
// names, so aren't among its verbs' aliases. npm's --dry-run is a setting
// of every command that changes what's installed, uninstall's too.
var npmLine = &kind.LineHelp{
	Program: "npm",
	Install: kind.LineVerb{Words: []string{"install"},
		Aliases: map[string][]string{"install": {"i", "in", "ins", "inst", "insta", "instal", "isnt", "isnta", "isntal", "isntall"}},
		DryRun:  dryRun,
		More:    globalOnly + " It runs from the home folder, as kit's own npm installs do, so under the default Node."},
	Uninstall: kind.LineVerb{Words: []string{"uninstall"},
		Aliases: map[string][]string{"uninstall": {"un", "r", "unlink"}},
		DryRun:  dryRun,
		More:    globalOnly + " A package's found with its version or without, as typescript for typescript@5."},
	Example:  "-g typescript@5",
	Declares: "as kit npm add declares it, with the version it's given, if any, kept for installing",
	Usage:    "-g <name>...",
	Names:    npmNames,
}

// globalOnly is why a line for npm needs -g.
const globalOnly = "The line needs -g or --global: kit declares npm's global packages only."

// dryRun is npm's and Composer's flag for a dry run.
var dryRun = []string{"--dry-run"}

// composerLine is how kit composer takes a line copied for composer: kit
// composer global require vendor/package.
var composerLine = &kind.LineHelp{
	Program: "composer",
	Install: kind.LineVerb{Words: []string{"global", "require"}, Aliases: map[string][]string{"global": {"g"}, "require": {"r"}}, DryRun: dryRun},
	Uninstall: kind.LineVerb{Words: []string{"global", "remove"}, Aliases: map[string][]string{"global": {"g"}, "remove": {"rm", "uninstall"}}, DryRun: dryRun,
		More: "A package's found with its constraint or without, as vendor/package for vendor/package:^2.0."},
	Example: "vendor/package:^2.0",
	Declares: "as kit composer add declares it, with the constraint it's given, if any, kept for installing, " +
		"as in vendor/package:^2.0, however the line gives it",
	Names: composerNames,
}

// ghLine is how kit gh takes a line copied for gh: kit gh extension
// install owner/gh-tool.
var ghLine = &kind.LineHelp{
	Program: "gh",
	Install: kind.LineVerb{Words: []string{"extension", "install"}, Aliases: map[string][]string{"extension": {"ext", "extensions"}},
		More: "An extension is installed by its repository, as owner/gh-tool: a name without its owner, as gh-tool, is refused."},
	Uninstall: kind.LineVerb{Words: []string{"extension", "remove"}, Aliases: map[string][]string{"extension": {"ext", "extensions"}, "remove": {"uninstall"}},
		More: "An extension can be given by its repository or, as gh takes it, by its command's name, as tool for owner/gh-tool."},
	Example:  "owner/gh-tool",
	Declares: "by its repository, as kit gh add declares it",
	Names:    ghNames,
}

var _ interface {
	kind.Kind
	kind.Keyed
	kind.Liner
	kind.Resolver
} = (*tool)(nil)

// Line runs l, a line copied for the program, as given, from where its own
// commands run, given the time its own install or uninstall of the names
// in it has, watched for going quiet, as an install is.
func (t *tool) Line(ctx context.Context, l kind.CopiedLine) error {
	c, words := t.remove, kind.UninstallWords
	if l.Installs {
		c, words = t.install, kind.InstallWords
	}
	timeout := c.timeout
	if names, _ := t.help.Line.Names(l.Args); c.each {
		timeout *= time.Duration(len(names))
	}
	cmd, stop := kind.Watch(ctx, words.Doing, t.command(timeout, slices.Concat(l.Verb, l.Args)))
	defer stop()
	_, err := t.run.Run(ctx, cmd)
	return err
}

// Refuses refuses an install line naming a thing by its short name alone,
// for a program that takes one (short), as gh's extension's command: the
// program installs by the full one.
func (t *tool) Refuses(_ context.Context, l kind.CopiedLine) error {
	if t.short == nil {
		return nil
	}
	names, _ := t.help.Line.Names(l.Args)
	var refused []error
	for _, name := range names {
		if !strings.Contains(name, "/") {
			refused = append(refused, fmt.Errorf("%s isn't a repository: give %s one, as owner/gh-%s", name, t.program, t.short(name)))
		}
	}
	return kind.Joined(refused)
}

// Resolve finds each of names' full names: its own, but for a short name,
// for a program that takes one (short), which is the full name of the one
// installed that has it, as gh's extension's command is its repository's;
// a short name none installed has is left out, as the kind doesn't know it.
func (t *tool) Resolve(ctx context.Context, names []string) (map[string]string, error) {
	resolved := make(map[string]string, len(names))
	var installed []string
	listed := false
	for _, name := range names {
		if t.short == nil || strings.Contains(name, "/") {
			resolved[name] = name
			continue
		}
		if !listed {
			var err error
			if installed, err = t.listed(t.exec(ctx, 0, t.list...)); err != nil {
				return nil, err
			}
			listed = true
		}
		if i := slices.IndexFunc(installed, func(full string) bool { return strings.EqualFold(t.short(full), t.short(name)) }); i >= 0 {
			resolved[name] = installed[i]
		}
	}
	return resolved, nil
}

// npmValued are npm's flags, of an install's or an uninstall's, that take
// the argument after them as their value, rather than after an =.
var npmValued = []string{
	"--install-strategy", "--omit", "--include", "--allow-directory", "--allow-file", "--allow-git", "--allow-remote",
	"--allow-scripts", "--before", "--min-release-age", "--min-release-age-exclude", "--cpu", "--os", "--libc",
	"-w", "--workspace", "--registry", "--prefix", "--tag", "--cache", "--userconfig", "--loglevel", "--location",
}

// npmPackage is a package from npm's registry, its scope and its version
// or not: what kit-config declares. A git repository, a tarball, a path
// or an alias of another package is none.
var npmPackage = regexp.MustCompile(`(?i)^(@[a-z0-9][\w.~-]*/)?[a-z0-9][\w.~-]*(@[^:/]*)?$`)

// npmNames are the packages in args, a line for npm install or uninstall,
// once it's global: its flags left out, and the value after one that takes
// it there. Anything else in place of a package is refused, as kit-config
// declares packages alone.
func npmNames(args []string) ([]string, error) {
	global := false
	var names []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-g" || arg == "--global" || arg == "--location=global":
			global = true
		case slices.Contains(npmValued, arg):
			if i++; arg == "--location" && i < len(args) && args[i] == "global" {
				global = true
			}
		case strings.HasPrefix(arg, "-"):
		case !npmPackage.MatchString(arg) || isTarball(arg):
			return nil, fmt.Errorf("%s isn't a package's name, which is all kit-config declares: give npm one, as in typescript or @scope/tool@2", arg)
		default:
			names = append(names, arg)
		}
	}
	switch {
	case !global:
		return nil, errors.New("the line isn't global: kit declares only npm's global packages, so give it -g")
	case len(names) == 0:
		return nil, errors.New("the line names nothing for npm: give it a package, as in kit npm install -g typescript")
	}
	return names, nil
}

// isTarball reports whether arg names a tarball's file, as npm takes one.
func isTarball(arg string) bool {
	return slices.ContainsFunc([]string{".tgz", ".tar", ".tar.gz"}, func(ext string) bool { return strings.HasSuffix(strings.ToLower(arg), ext) })
}

// composerValued are Composer's flags, of require's or remove's, that take
// the argument after them as their value, rather than after an =.
var composerValued = []string{"--prefer-install", "--audit-format", "--ignore-platform-req", "--apcu-autoloader-prefix", "-d", "--working-dir"}

// composerPackage is a package's name, as vendor/package.
var composerPackage = regexp.MustCompile(`(?i)^[a-z0-9]([\w.-]*[a-z0-9])?/[a-z0-9]([\w.-]*[a-z0-9])?$`)

// composerNames are the packages in args, a line for composer global
// require or remove, each with the constraint it's given, if any, as kit
// composer add declares one: vendor/package:^2.0, whether the line gives
// it after a colon, an =, a space or as the next argument, as Composer
// pairs them. Anything else in place of a package is refused.
func composerNames(args []string) ([]string, error) {
	var names []string
	constrained := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, constraint, paired := arg, "", false
		if at := strings.IndexAny(arg, ":= "); at >= 0 {
			name, constraint, paired = arg[:at], strings.TrimSpace(arg[at+1:]), true
		}
		switch {
		case slices.Contains(composerValued, arg):
			i++
		case strings.HasPrefix(arg, "-"):
		case composerPackage.MatchString(name):
			names = append(names, name)
			if constrained = paired; paired {
				names[len(names)-1] += ":" + constraint
			}
		case !constrained && !strings.Contains(arg, "/"):
			names[len(names)-1] += ":" + arg
			constrained = true
		default:
			return nil, fmt.Errorf("%s isn't a package's name, which is all kit-config declares: give composer one, as in vendor/package", arg)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("the line names nothing for composer: give it a package, as in kit composer global require vendor/package")
	}
	return names, nil
}

// ghNames are the extensions in args, a line for gh extension install or
// remove: its flags left out, and the value after --pin. An address, or a
// folder, as in gh extension install ., is refused, as kit-config declares
// a repository on GitHub, as owner/repo.
func ghNames(args []string) ([]string, error) {
	var names []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--pin":
			i++
		case strings.HasPrefix(arg, "-"):
		case strings.Contains(arg, "://") || strings.Count(arg, "/") > 1 || strings.HasPrefix(arg, ".") || strings.HasPrefix(arg, "~"):
			return nil, fmt.Errorf("%s is an address or a folder, which kit-config can't declare: give gh a repository on GitHub, as owner/gh-tool", arg)
		default:
			names = append(names, arg)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("the line names nothing for gh: give it an extension, as in kit gh extension install owner/gh-tool")
	}
	return names, nil
}
