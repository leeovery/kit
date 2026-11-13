// Package restore is a new Mac's data restore: the paths in the home folder
// a Mac's [restore] section lists, folders or files, carried from the Mac it
// started from, or from a backup of it. What's kit's in a path (the files it
// links, what prefs saves, what kinds install, what the backups leave out,
// kit's own folders) is never copied, and never counts as the path's holding
// anything: kit puts it there itself. Each path is judged before any copy,
// and again as its copy's turn comes, by its record, else by what it holds:
// restored once, resumed, restored after what it holds is set aside on a
// Mac in set-up, or, holding files of its own, left as it is, here, for
// good. A path its source hasn't is done with, not there, till a source
// that has it is named.
package restore
