package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/restore"
	"github.com/leeovery/kit/internal/steps"
)

// Folder is one of the home folder's own folders a new Mac would bring back
// from backups.
type Folder struct {
	// Path is where it is, from ~, as in ~/Code.
	Path string
	// Size is what it holds on disk, in bytes; Whole says that's all of
	// it, rather than as much as was counted in the time given, or could be
	// read. Err is why some of it couldn't be read, as without macOS's
	// permission.
	Size  int64
	Whole bool
	Err   error
	// Ticked is whether the question suggests bringing it back.
	Ticked bool
}

// notData are the home folder's own folders that never hold the person's
// data: macOS's Library, and the apps in Applications, which are the
// kinds' or installed by hand.
var notData = []string{"Library", "Applications"}

// unticked are the folders macOS makes for what comes from elsewhere,
// downloads and media, and for sharing, which the question suggests
// leaving in the backups; every other folder is the person's own work, as
// Documents and a folder of code are, and is ticked.
var unticked = []string{"Downloads", "Movies", "Music", "Public"}

// linkable are the usual places, from ~, of the files a person writes for
// the shell and their tools. Never git's settings file: git's settings are
// declared as a kind, and a path has one owner.
var linkable = []string{
	".zshrc", ".zprofile", ".zshenv", ".zlogin", ".bashrc", ".bash_profile", ".profile", ".inputrc",
	".vimrc", ".tmux.conf", ".config/tmux/tmux.conf", ".ssh/config", ".config/ghostty/config",
	".config/starship.toml", ".editorconfig",
}

// owned are the paths in the home folder that are kit's on the Mac, as the
// restore reads them: no data folder, nor file to link, can be one.
func (m Mac) owned(ctx context.Context, deps *kind.Deps) (restore.Owned, error) {
	features, err := m.Config.List(config.FeaturesKind, m.Name)
	if err != nil {
		return restore.Owned{}, err
	}
	ks := make([]kind.Kind, len(kinds.All))
	for i, d := range kinds.All {
		ks[i] = d.New(deps)
	}
	files := &linked.Files{Repo: gitrepo.Repo{Dir: m.Config.Dir, Run: m.Run}, Home: m.Home, StateDir: m.Dirs.State, Scopes: []string{config.Shared, m.Name}}
	return restore.ReadOwned(ctx, restore.Mac{
		Config: m.Config, Name: m.Name, Home: m.Home, Dirs: m.Dirs, Linked: files,
		Prefs: slices.Contains(features.Names(), steps.FeaturePrefs), Kinds: ks,
	})
}

// folders are the home folder's own folders, unsized, that hold the
// person's data: not hidden, not links, none of notData or kit's, and on
// the home folder's volume, as a volume mounted there, as an app's view of
// its virtual machines, isn't the backups'.
func (m Mac) folders(owned restore.Owned) ([]Folder, error) {
	entries, err := os.ReadDir(m.Home)
	if err != nil {
		return nil, fmt.Errorf("read the home folder: %w", err)
	}
	home, err := lstat(m.Home)
	if err != nil {
		return nil, fmt.Errorf("read the home folder: %w", err)
	}
	var out []Folder
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || slices.Contains(notData, name) {
			continue
		}
		if st, err := lstat(filepath.Join(m.Home, name)); err == nil && st.Dev != home.Dev {
			continue
		}
		p := "~/" + name
		if isKits(owned, p) {
			continue
		}
		out = append(out, Folder{Path: p, Ticked: !slices.Contains(unticked, name)})
	}
	return out, nil
}

// files are those of linkable that are files of the Mac's own, not links,
// nor kit's already: linked, saved by prefs, filled by a secret.
func (m Mac) files(owned restore.Owned) []string {
	var out []string
	for _, rel := range linkable {
		p := "~/" + rel
		info, err := os.Lstat(filepath.Join(m.Home, rel))
		if err == nil && info.Mode().IsRegular() && !isKits(owned, p) {
			out = append(out, p)
		}
	}
	return out
}

// isKits reports whether p, from ~, is kit's, or in what's kit's, as the
// restore judges a path: one owner a path.
func isKits(owned restore.Owned, p string) bool {
	_, err := restore.NewFilter(restore.Line{Path: p}, owned)
	return err != nil
}
