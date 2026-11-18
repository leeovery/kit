# art: still
# A still for the README's Screens: kit reconcile asking what to do with
# each thing that drifted. capturetool draws the reconcile fixture
# (internal/capture/stills.go), a window of its own, and holds it once it's
# drawn.
#
# Record it, and make art/reconcile.webp:
#   demo/record.sh reconcile

Output "demo/out/reconcile/"
Source "demo/look.tape"
# 100 × 24 cells of 13 × 29 px: as look.tape works it out.
Set Width 1360
Set Height 740
Hide
Type "clear && demo/out/capturetool --fixture reconcile"
Enter
Wait+Screen /RECONCILE  2 of 3/
Show
Sleep 500ms
Hide
Enter
