// Package reinstall is the dependencies of the projects a new Mac's restore
// brings back, installed again: the restore copies a project without them,
// as the backups leave them out. It finds the projects in each path the
// restore has done, once, by their lockfiles, and runs each one's package
// manager in its folder, recording how each came out in kit's state.
package reinstall

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// tool is a package manager a restored project's dependencies reinstall
// with: its program; its lockfiles, any one of which in a folder makes the
// folder's dependencies its; what in the folder, there, says they're
// installed; its arguments, installing just what the lockfile says,
// scripts and all; whether it installs the folder's JavaScript packages,
// which one tool alone does; and, when it can't install a folder's, what the
// person's to do instead (yours), "" when it can.
type tool struct {
	program   string
	lockfiles []string
	installed []string
	args      []string
	js        bool
	yours     func(dir string) string
}

// tools are the package managers reinstalling runs, in the order a
// folder's run: of those for JavaScript, the first whose lockfile the
// folder holds, as a stray package-lock.json is more often left beside
// another's than the other way round. Terraform's, Swift's, Go's and
// Cargo's are left alone: each fetches what it needs as it builds.
var tools = []tool{
	{program: "composer", lockfiles: []string{"composer.lock"}, installed: []string{"vendor"}, args: []string{"install", "--no-interaction"}},
	{program: "pnpm", lockfiles: []string{"pnpm-lock.yaml"}, installed: []string{"node_modules"}, args: []string{"install", "--frozen-lockfile"}, js: true},
	{program: "bun", lockfiles: []string{"bun.lock", "bun.lockb"}, installed: []string{"node_modules"}, args: []string{"install", "--frozen-lockfile"}, js: true},
	{program: "yarn", lockfiles: []string{"yarn.lock"}, installed: []string{"node_modules"}, args: []string{"install", "--frozen-lockfile"}, js: true, yours: modernYarnYours},
	{program: "npm", lockfiles: []string{"package-lock.json"}, installed: []string{"node_modules"}, args: []string{"ci"}, js: true},
	{program: "uv", lockfiles: []string{"uv.lock"}, installed: []string{".venv"}, args: []string{"sync", "--frozen"}},
}

// modernYarnYours is what the person's to do in dir when its yarn.lock is
// Yarn 2's or later's: Homebrew's yarn is Yarn 1, which runs the project's
// own Yarn from .yarn/releases, a folder the backups leave out, as they do
// all of .yarn beside a .yarnrc.yml.
func modernYarnYours(dir string) string {
	if modernYarn(filepath.Join(dir, "yarn.lock")) {
		return "Yarn 2 or later: run yarn install in its folder"
	}
	return ""
}

// modernYarn reports whether the yarn.lock at path is Yarn 2's or later's,
// from its first lines.
func modernYarn(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 1024)
	n, _ := io.ReadFull(f, head)
	return bytes.Contains(head[:n], []byte("\n__metadata:"))
}

// toolNamed is the tool whose program is name.
func toolNamed(name string) (tool, bool) {
	i := slices.IndexFunc(tools, func(t tool) bool { return t.program == name })
	if i < 0 {
		return tool{}, false
	}
	return tools[i], true
}

// installedIn reports whether t's dependencies are there in dir: a folder
// copied whole, or installed by hand.
func (t tool) installedIn(dir string) bool {
	return slices.ContainsFunc(t.installed, func(name string) bool {
		_, err := os.Lstat(filepath.Join(dir, name))
		return err == nil
	})
}

// searchDepth is how many folders down from a restored folder the search
// goes at most: a limit for safety alone, as passing over dependency and
// hidden folders is what keeps it short, and projects sit deep, as in
// ~/Code/team/app/projects/agent or …/javascript/apps/web.
const searchDepth = 12

// Find finds the projects in path, a path the restore's done, from ~, in
// the home folder home, and the install each lockfile in them asks for,
// each folder from ~. Each is judged as it's found, never later, as an
// install stopped part-way leaves its dependency folder begun: one whose
// dependency folder is there already, copied whole or installed by hand,
// is There; one its tool can't install, the person's (Yours). Linked
// worktrees are projects as main checkouts are; submodules, whose .git is
// a file pointing into another's .git/modules, are passed over, all in
// them; nor does the search go into a dependency folder (node_modules,
// vendor) or a hidden one (.git, .venv, agents' worktrees in
// .claude/worktrees, tools' caches). A project nested in another, as a
// monorepo's package, is one of its own only when it holds a lockfile of
// its own. A path that's a file holds none.
func Find(home, path string) ([]Install, error) {
	root := filepath.Join(home, strings.TrimPrefix(path, "~/"))
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, nil
	}
	var found []Install
	err := search(root, 0, func(dir string, beside map[string]fs.DirEntry) {
		rel, _ := filepath.Rel(home, dir)
		for _, t := range toolsFor(beside) {
			found = append(found, judged(t, dir, Install{Dir: "~/" + filepath.ToSlash(rel), Tool: t.program}))
		}
	})
	return found, err
}

// judged is in, of t in dir, as it's found: There, its dependencies there;
// Yours, when t can't install them, saying what to do; else to run.
func judged(t tool, dir string, in Install) Install {
	switch {
	case t.installedIn(dir):
		in.Outcome = There
	case t.yours != nil:
		if do := t.yours(dir); do != "" {
			in.Outcome, in.Error = Yours, do
		}
	}
	return in
}

// search visits dir, searchDepth folders down at most, depth from where the
// search started, then each folder in it the search goes into, as Find
// says: visit is given each folder and what's in it, by name. A folder in
// it that can't be read is passed over.
func search(dir string, depth int, visit func(dir string, beside map[string]fs.DirEntry)) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if depth > 0 && errors.Is(err, fs.ErrPermission) {
			return nil
		}
		return fmt.Errorf("look for projects in %s: %w", dir, err)
	}
	beside := make(map[string]fs.DirEntry, len(entries))
	for _, e := range entries {
		beside[e.Name()] = e
	}
	if git, ok := beside[".git"]; ok && !git.IsDir() && submodule(filepath.Join(dir, ".git")) {
		return nil
	}
	visit(dir, beside)
	if depth == searchDepth {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() && !passedOver(e.Name()) {
			if err := search(filepath.Join(dir, e.Name()), depth+1, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// submodule reports whether the .git file at path is a submodule's: its
// gitdir in another repository's .git/modules, where a linked worktree's
// is in its .git/worktrees.
func submodule(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), "/.git/modules/")
}

// passedOver reports whether the search doesn't go into the folder name: a
// hidden folder, or one of dependencies, installed or, as Go's vendor,
// committed, which holds others' projects, never the person's.
func passedOver(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor"
}

// toolsFor are the tools a folder's lockfiles, among what's beside, ask
// for, in tools' order: one at most for its JavaScript packages.
func toolsFor(beside map[string]fs.DirEntry) []tool {
	var out []tool
	js := false
	for _, t := range tools {
		if t.js && js {
			continue
		}
		if slices.ContainsFunc(t.lockfiles, func(name string) bool { _, ok := beside[name]; return ok }) {
			out = append(out, t)
			js = js || t.js
		}
	}
	return out
}
