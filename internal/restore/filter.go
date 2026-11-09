package restore

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// Filter is what restoring a line's path leaves out: what's kit's in it,
// what the line skips, and Finder's .DS_Store and .localized wherever they
// are. It's rsync's filter rules, anchored at the path, written for rsync
// (Write) and matched in Go (Excludes) from the same rules, so the copy and
// the judgement of what a path holds can't disagree.
type Filter struct {
	Line  Line
	rules []rule
}

// finder are the files Finder leaves in folders.
var finder = []string{".DS_Store", ".localized"}

// NewFilter is line's filter, kit's paths being owned. A line whose path is
// kit's, or in one of kit's, is refused, naming the line: the restore never
// carries it.
func NewFilter(line Line, owned Owned) (*Filter, error) {
	f := &Filter{Line: line}
	var texts []string
	for _, o := range owned.owners() {
		text, covers := o.under(line.Path)
		if covers {
			return nil, fmt.Errorf("%s: the restore never carries %s: %s", line.Pos, line.Path, o.why)
		}
		if text != "" {
			texts = append(texts, text)
		}
	}
	for _, skip := range line.Skips {
		texts = append(texts, "/"+skip)
	}
	for _, text := range slices.Concat(texts, finder) {
		if strings.ContainsAny(text, "\r\n") {
			return nil, fmt.Errorf("%s: %q, in %s, can't be one of rsync's rules: a rule is a line", line.Pos, text, line.Path)
		}
		if !slices.ContainsFunc(f.rules, func(r rule) bool { return r.text == text }) {
			f.rules = append(f.rules, newRule(text))
		}
	}
	return f, nil
}

// Write writes f's rules into the file at path, for rsync's --exclude-from:
// a rule a line, "- " and its pattern. Those starting with / are anchored at
// the line's path, so rsync's transfer is rooted there: for a folder, its
// source and destination each given with a trailing /.
func (f *Filter) Write(path string) error {
	var b strings.Builder
	for _, r := range f.rules {
		b.WriteString("- " + r.text + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write the restore's filter: %w", err)
	}
	return nil
}

// Excludes reports whether restoring f's line leaves out rel, a path in the
// line's path, its parts separated by /, a folder when dir is: when a rule
// matches it, or a folder it's in, as rsync never looks inside a folder it
// leaves out.
func (f *Filter) Excludes(rel string, dir bool) bool {
	for i, r := range rel {
		if r == '/' && f.leaves(rel[:i], true) {
			return true
		}
	}
	return f.leaves(rel, dir)
}

// leaves reports whether one of f's rules matches rel itself.
func (f *Filter) leaves(rel string, dir bool) bool {
	return slices.ContainsFunc(f.rules, func(r rule) bool { return r.matches(rel, dir) })
}

// rule is one of rsync's filter rules, leaving out what its pattern
// matches, as kit writes them: anchored at the transfer's root, starting
// with /, or a name alone, matched wherever it is.
type rule struct {
	// text is the rule as rsync reads it, after its "- ".
	text string
	// pattern is what's matched: text without the / anchoring it or one
	// ending it.
	pattern string
	// anchored is whether it starts with /; folders whether it ends with /,
	// matching folders alone; wild whether it holds *, ? or [, making it a
	// pattern, not a name; wild3 whether it ends with ***, matching a
	// folder as well as everything in it.
	anchored, folders, wild, wild3 bool
}

// newRule reads text as rsync reads a rule's pattern.
func newRule(text string) rule {
	r := rule{text: text, pattern: text}
	if len(r.pattern) > 1 {
		r.pattern, r.folders = strings.CutSuffix(r.pattern, "/")
	}
	r.wild = hasMagic(r.pattern)
	r.wild3 = r.wild && strings.HasSuffix(r.pattern, "***")
	r.pattern, r.anchored = strings.CutPrefix(r.pattern, "/")
	return r
}

// matches reports whether r matches name, a path from the transfer's root,
// a folder when dir is, as rsync's rule_matches does: an anchored rule the
// whole path; a name alone, the path's last part.
func (r rule) matches(name string, dir bool) bool {
	if r.folders && !dir {
		return false
	}
	if !r.anchored {
		name = name[strings.LastIndexByte(name, '/')+1:]
	}
	if dir && r.wild3 {
		name += "/"
	}
	if !r.wild {
		return name == r.pattern
	}
	return wildmatch(r.pattern, name)
}
