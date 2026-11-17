# art: still
# A still for the README: kit status on a Mac in daily use, two things
# waiting on the person, printed as kit prints it. kit is the shim in
# demo/bin, the capture tool drawing the status fixture.
#
# Record it, and make art/status.webp:
#   demo/record.sh status

Output "demo/out/status/"
Source "demo/look.tape"
# kit's report goes no wider than 80 columns: 84 at 30px, cells of 18 × 40,
# keep it as wide as the clip, and its 28 rows, the command and the prompt
# with it, fit 34.
Set FontSize 30
Set Width 1600
Set Height 1402

Hide
Type `export PATH="$PWD/demo/bin:/usr/bin:/bin" PS1='\[\e[34m\]❯\[\e[0m\] ' && clear`
Enter
Wait
Show

Sleep 600ms
Type "kit status"
Sleep 400ms
Enter
# The report's last line, which only the fixture's whole run prints.
Wait+Screen /2 need you · 13 fine · 1\.6s/
Wait
Sleep 800ms
