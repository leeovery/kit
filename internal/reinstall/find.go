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
// installed; its arguments in the folder, installing just what the lockfile
// says, scripts and all; and whether it installs the folder's JavaScript
// packages, which one tool alone does.
type tool struct {
	program   string
	lockfiles []string
	installed []string
	args      func(dir string) []string
	js        bool
}

// tools are the package managers reinstalling runs, in the order a
// folder's run: of those for JavaScript, the first whose lockfile the
// folder holds, as a stray package-lock.json is more often left beside
// another's than the other way round. Terraform's, Swift's, Go's and
// Cargo's are left alone: each fetches what it needs as it builds.
var tools = []tool{
	{program: "composer", lockfiles: []string{"composer.lock"}, installed: []string{"vendor"}, args: always("install", "--no-interaction")},
	{program: "pnpm", lockfiles: []string{"pnpm-lock.yaml"}, installed: []string{"node_modules"}, args: always("install", "--frozen-lockfile"), js: true},
	{program: "bun", lockfiles: []string{"bun.lock", "bun.lockb"}, installed: []string{"node_modules"}, args: always("install", "--frozen-lockfile"), js: true},
	{program: "yarn", lockfiles: []string{"yarn.lock"}, installed: []string{"node_modules", ".pnp.cjs"}, args: yarnArgs, js: true},
	{program: "npm", lockfiles: []string{"package-lock.json"}, installed: []string{"node_modules"}, args: always("ci"), js: true},
	{program: "uv", lockfiles: []string{"uv.lock"}, installed: []string{".venv"}, args: always("sync", "--frozen")},
}

// always is a tool's arguments, the same in every folder.
func always(args ...string) func(string) []string {
	return func(string) []string { return args }
}

// yarnArgs are yarn's arguments in dir: Yarn 2 and later, whose lockfile
// starts with its metadata, take --immutable; Yarn 1 --frozen-lockfile.
func yarnArgs(dir string) []string {
	if modernYarn(filepath.Join(dir, "yarn.lock")) {
		return []string{"install", "--immutable"}
	}
	return []string{"install", "--frozen-lockfile"}
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

// searchDepth is how many folders down from a restored folder projects are
// looked for, as in ~/Code/team/app/packages/web.
const searchDepth = 4

// Find finds the projects in path, a path the restore's done, from
// ~, in the home folder home, and the install each lockfile in them asks
// for, each folder from ~. A folder whose .git is a file is a linked
// worktree, or a submodule, and is passed over, all in it; nor does the
// search go into a dependency folder (node_modules; vendor beside a
// composer.json) or a hidden one (.git, .venv, tools' caches). A project
// nested in another, as a monorepo's package, is one of its own only when
// it holds a lockfile of its own. A path that's a file holds none.
func Find(home, path string) ([]Install, error) {
	root := filepath.Join(home, strings.TrimPrefix(path, "~/"))
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, nil
	}
	var found []Install
	err := search(root, 0, func(dir string, beside map[string]fs.DirEntry) {
		rel, _ := filepath.Rel(home, dir)
		for _, t := range toolsFor(beside) {
			found = append(found, Install{Dir: "~/" + filepath.ToSlash(rel), Tool: t.program})
		}
	})
	return found, err
}

// search visits dir, searchDepth folders down at most, depth from where the
// search started, then each folder in it the search goes into, as
// findInstalls says: visit is given each folder and what's in it, by name.
// A folder in it that can't be read is passed over.
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
	if git, ok := beside[".git"]; ok && !git.IsDir() {
		return nil
	}
	visit(dir, beside)
	if depth == searchDepth {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() && !passedOver(e.Name(), beside) {
			if err := search(filepath.Join(dir, e.Name()), depth+1, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// passedOver reports whether the search doesn't go into the folder name,
// beside what's with it: a hidden folder, or one of dependencies.
func passedOver(name string, beside map[string]fs.DirEntry) bool {
	switch {
	case strings.HasPrefix(name, "."), name == "node_modules":
		return true
	case name == "vendor":
		_, composer := beside["composer.json"]
		return composer
	}
	return false
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
