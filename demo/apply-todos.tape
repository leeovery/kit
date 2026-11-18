# art: still
# A still for the README's Screens: the to-dos at kit apply's end, the first
# done, the next one's card up. capturetool draws the apply-todos fixture
# (internal/capture/stills.go), a window of its own, and holds it once it's
# drawn.
#
# Record it, and make art/apply-todos.webp:
#   demo/record.sh apply-todos

Output "demo/out/apply-todos/"
Source "demo/look.tape"

Hide
Type "clear && demo/out/capturetool --fixture apply-todos"
Enter
Wait+Screen /It isn.t in Homebrew/
Show
Sleep 500ms
Hide
Enter
