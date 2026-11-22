package brew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Casked is what the installed casks put in the Applications folders.
type Casked struct {
	// Apps are the apps their app artifacts install, by name, as in
	// Example.app; Suites the folders of apps their suite artifacts do, by
	// name: each renamed by the target given after it, if one is.
	Apps, Suites []string
	// Receipts are the ids of the packages they installed, as their
	// uninstall names them, each a pattern as Homebrew matches pkgutil's
	// ids.
	Receipts []string
}

// Casked is what the installed casks put in place, each as its install
// receipt in Homebrew's Caskroom (brew --caskroom) says it installed, read
// from the file; brew info, a second or more slower, says it of those
// whose receipt doesn't.
func (h *Homebrew) Casked(ctx context.Context) (Casked, error) {
	res, err := h.brew(ctx, "--caskroom")
	if err != nil {
		return Casked{}, err
	}
	room := strings.TrimSpace(string(res.Stdout))
	entries, err := os.ReadDir(room)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Casked{}, fmt.Errorf("read Homebrew's Caskroom: %w", err)
	}
	var c Casked
	var unsaid []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if artifacts, ok := installedArtifacts(filepath.Join(room, e.Name())); ok {
			c.add(artifacts)
		} else {
			unsaid = append(unsaid, e.Name())
		}
	}
	if len(unsaid) == 0 {
		return c, nil
	}
	res, err = h.brew(ctx, slices.Concat([]string{"info", "--json=v2", "--cask"}, unsaid)...)
	if err != nil {
		return Casked{}, err
	}
	var in info
	if err := json.Unmarshal(res.Stdout, &in); err != nil {
		return Casked{}, fmt.Errorf("read brew info's answer: %w", err)
	}
	for _, cask := range in.Casks {
		c.add(cask.Artifacts)
	}
	return c, nil
}

// add adds what a cask's artifacts put in place to c.
func (c *Casked) add(artifacts []map[string]json.RawMessage) {
	c.Apps = append(c.Apps, installs(artifacts, "app")...)
	c.Suites = append(c.Suites, installs(artifacts, "suite")...)
	c.Receipts = append(c.Receipts, receiptsOf(artifacts)...)
}

// installedArtifacts are the artifacts the cask installed in dir, in the
// Caskroom, as its install receipt says, in brew info's form: none when it
// can't be read, or doesn't say.
func installedArtifacts(dir string) ([]map[string]json.RawMessage, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ".metadata", "INSTALL_RECEIPT.json"))
	if err != nil {
		return nil, false
	}
	var receipt struct {
		Artifacts []map[string]json.RawMessage `json:"uninstall_artifacts"`
	}
	if json.Unmarshal(data, &receipt) != nil || receipt.Artifacts == nil {
		return nil, false
	}
	return receipt.Artifacts, true
}

// installs are what a cask's artifacts of the sort given install, by name:
// each named, or by the target given after it.
func installs(artifacts []map[string]json.RawMessage, sort string) []string {
	var names []string
	for _, artifact := range artifacts {
		var parts []json.RawMessage
		if json.Unmarshal(artifact[sort], &parts) != nil {
			continue
		}
		for _, part := range parts {
			var name string
			var target struct {
				Target string `json:"target"`
			}
			switch {
			case json.Unmarshal(part, &name) == nil:
				names = append(names, filepath.Base(name))
			case json.Unmarshal(part, &target) == nil && target.Target != "" && len(names) > 0:
				names[len(names)-1] = filepath.Base(target.Target)
			}
		}
	}
	return names
}

// receiptsOf are the package ids a cask's uninstall names, its pkgutil's:
// each one, or a list of them.
func receiptsOf(artifacts []map[string]json.RawMessage) []string {
	var ids []string
	for _, artifact := range artifacts {
		var steps []map[string]json.RawMessage
		if json.Unmarshal(artifact["uninstall"], &steps) != nil {
			continue
		}
		for _, step := range steps {
			ids = append(ids, oneOrMore(step["pkgutil"])...)
		}
	}
	return ids
}
