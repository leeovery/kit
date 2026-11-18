# art: part of new-mac
# The README's lead, its second part: the boot carrying on in Ghostty,
# where kit's own look arrives. capturetool plays the arrival scenario
# (internal/capture/scenarios.go) in real time: "hello again", a key
# pressed; the decode in colour, the wordmark locking in kit's gradient and
# shrinking into the header's corner, its line typed in; then kit apply's
# screen under it, flying through the run: 1Password's sign-in coming up on
# the right and done, Homebrew counting through its installs, the to-dos at
# the end worked through, and the run's record.
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
Wait+Screen@40s /kit apply brings them back/
Sleep 2500ms
Hide
Enter
