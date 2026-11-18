# art: part of new-mac
# The README's lead, its first part: a new Mac's boot in Terminal, before
# anything's installed. capturetool plays the boot scenario
# (internal/capture/scenarios.go) through kit's own boot face, in real time:
# the splash's decode, enter pressed at "press enter"; the self-test; signing
# in to GitHub with the phone, its QR code the whole window; the boot order,
# This Mac chosen, the password typed, Homebrew at work, Full Disk Access
# done by hand, to Ghostty opening and "this window can be closed".
# new-mac-2-ghostty.tape carries on in Ghostty.
#
# Record both, and make art/new-mac.png, and demo/out/new-mac.mp4:
#   demo/record.sh new-mac

Output "demo/out/new-mac-1-terminal/"
Source "demo/look-terminal.tape"

Hide
# The scenario waits for a line on stdin, the screen blank, so the clip
# starts on its first frame.
Type "clear && demo/out/capturetool --scenario boot"
Enter
Sleep 1s
Show
Sleep 300ms
Enter
Wait+Screen@40s /this window can be closed/
Sleep 1200ms
Hide
Enter
