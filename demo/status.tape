# art: still
# A still for the README: kit status, the pretty face's report on a Mac
# with some of everything, printed as kit prints it. kit is the shim in
# demo/bin, the capture tool drawing the status fixture: the run
# internal/render/testdata/pretty-colour.golden holds.
#
# Record it, and make art/status.webp:
#   demo/record.sh status

Output "demo/out/status/"
Source "demo/look.tape"
# kit's report goes no wider than 80 columns: 84 at 30px, cells of 18 × 40,
# keep it as wide as the clip, and its 32 rows, the command and the prompt
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
Wait+Screen /1 failed · 2 need you · 1 not checked · 6 fine · 1\.8s/
Wait
Sleep 800ms
