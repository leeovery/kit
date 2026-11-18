package brew

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
)

// Casked is what the installed casks put in place, as brew info says: the
// apps their app artifacts install, by name, as in Example.app, a target
// renaming one as it's installed; and, for the casks installing through a
// package, its receipts' ids as their uninstall names them, each a pattern
// as Homebrew matches pkgutil's ids.
func (h *Homebrew) Casked(ctx context.Context) (apps, receipts []string, err error) {
	res, err := h.brew(ctx, "info", "--json=v2", "--cask", "--installed")
	if err != nil {
		return nil, nil, err
	}
	var in info
	if err := json.Unmarshal(res.Stdout, &in); err != nil {
		return nil, nil, fmt.Errorf("read brew info's answer: %w", err)
	}
	for _, cask := range in.Casks {
		apps = append(apps, appsOf(cask.Artifacts)...)
		if slices.ContainsFunc(cask.Artifacts, func(a map[string]json.RawMessage) bool { return a["pkg"] != nil }) {
			receipts = append(receipts, receiptsOf(cask.Artifacts)...)
		}
	}
	return apps, receipts, nil
}

// appsOf are the apps a cask's app artifacts install, by name: each named,
// or by the target given after it.
func appsOf(artifacts []map[string]json.RawMessage) []string {
	var apps []string
	for _, artifact := range artifacts {
		var parts []json.RawMessage
		if json.Unmarshal(artifact["app"], &parts) != nil {
			continue
		}
		for _, part := range parts {
			var name string
			var target struct {
				Target string `json:"target"`
			}
			switch {
			case json.Unmarshal(part, &name) == nil:
				apps = append(apps, filepath.Base(name))
			case json.Unmarshal(part, &target) == nil && target.Target != "" && len(apps) > 0:
				apps[len(apps)-1] = filepath.Base(target.Target)
			}
		}
	}
	return apps
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
