// Package restore is a new Mac's data restore: the paths in the home folder
// a Mac's [restore] section lists, folders or files, carried from the Mac it
// started from, or from a backup of it. What's kit's in a path (the files it
// links, what prefs saves, what kinds install, what the backups leave out,
// kit's own folders) is never copied, and never counts as the path's holding
// anything: kit puts it there itself. Each path is judged before any copy,
// by its record, else by what it holds: restored once, resumed, restored
// after what set-up wrote into it is set aside, or, holding older files,
// left as it is, here, for good.
package restore
