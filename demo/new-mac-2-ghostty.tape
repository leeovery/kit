# art: part of new-mac
# The README's lead, its second part: the boot carrying on in Ghostty,
# where kit's own look arrives. capturetool plays the arrival scenario
# (internal/capture/scenarios.go) in real time: "hello again", a key
# pressed; the decode in colour, the wordmark locking in kit's gradient and
# shrinking into the header's corner, its line typed in; then kit apply's
# screen under it, rows at work, 1Password's sign-in coming up on the
# right, and Claude Code's step finishing.
#
# Recorded with new-mac-1-terminal.tape: demo/record.sh new-mac

Output "demo/out/new-mac-2-ghostty/"
Source "demo/look.tape"

Hide
Type "clear && demo/out/capturetool --scenario arrival"
Enter
Sleep 1s
Show
Sleep 300ms
Enter
Wait+Screen@40s /set up · 3 plugins/
Sleep 1200ms
Hide
Enter
