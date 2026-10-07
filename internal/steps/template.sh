#!/bin/bash
# STEP: WHAT IT DOES
#
# A step of your own. kit runs this as `run check` or `run apply`, in this
# folder, with nothing on its input:
# - check says how it stands, and changes nothing: exit 0 when it's done, 1
#   when it isn't, the first line it prints saying how it stands.
# - apply does it, safe to run again; kit checks again afterwards. When it
#   fails, the last line it prints says why.
# The folder's other files are the step's own data. A step declared --admin
# may use sudo -n: kit has asked for the password before applying.
set -euo pipefail

check() {
	echo "not written yet"
	return 2
}

apply() {
	echo "not written yet"
	return 1
}

case "${1:-}" in
check | apply) "$1" ;;
*)
	echo "usage: run check|apply" >&2
	exit 2
	;;
esac
