package brew

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
)

// Casked is what the installed casks put in the Applications folders.
type Casked struct {
	// Apps are the apps their app artifacts install, by name, as in
	// Example.app; Suites the folders of apps their suite artifacts do, by
	// name: each renamed by the target given after it, if one is.
	Apps, Suites []string
	// Receipts are, for the casks installing through a package, its
	// receipts' ids as their uninstall names them, each a pattern as
	// Homebrew matches pkgutil's ids.
	Receipts []string
}

// Casked is what the installed casks put in place, as brew info says.
func (h *Homebrew) Casked(ctx context.Context) (Casked, error) {
	res, err := h.brew(ctx, "info", "--json=v2", "--cask", "--installed")
	if err != nil {
		return Casked{}, err
	}
	var in info
	if err := json.Unmarshal(res.Stdout, &in); err != nil {
		return Casked{}, fmt.Errorf("read brew info's answer: %w", err)
	}
	var c Casked
	for _, cask := range in.Casks {
		c.Apps = append(c.Apps, installs(cask.Artifacts, "app")...)
		c.Suites = append(c.Suites, installs(cask.Artifacts, "suite")...)
		if slices.ContainsFunc(cask.Artifacts, func(a map[string]json.RawMessage) bool { return a["pkg"] != nil }) {
			c.Receipts = append(c.Receipts, receiptsOf(cask.Artifacts)...)
		}
	}
	return c, nil
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
