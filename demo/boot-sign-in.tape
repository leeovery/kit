# art: still
# A still for the README's Screens: the boot signing in to GitHub in
# Terminal, its QR code the whole window. capturetool draws the boot-sign-in
# fixture (internal/capture/stills.go), a window of its own, and holds it
# once it's drawn.
#
# Record it, and make art/boot-sign-in.webp:
#   demo/record.sh boot-sign-in

Output "demo/out/boot-sign-in/"
Source "demo/look-terminal.tape"

Hide
Type "clear && demo/out/capturetool --fixture boot-sign-in"
Enter
Wait+Screen /WDJB-MJHT/
Show
Sleep 500ms
Hide
Enter
