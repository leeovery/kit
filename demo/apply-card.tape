# art: still
# A still for the README's Screens: kit apply's screen, 1Password's sign-in
# come up as a card. capturetool draws the apply-card fixture
# (internal/capture/stills.go), a window of its own, and holds it once it's
# drawn.
#
# Record it, and make art/apply-card.webp:
#   demo/record.sh apply-card

Output "demo/out/apply-card/"
Source "demo/look.tape"

Hide
Type "clear && demo/out/capturetool --fixture apply-card"
Enter
Wait+Screen /kit carries on by itself/
Show
Sleep 500ms
Hide
Enter
