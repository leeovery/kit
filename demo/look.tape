# The look every tape shares, sourced first: Ghostty, JetBrains Mono in
# its theme, at 120 × 34: Terminal's window on a new Mac, which the boot is
# made for, 120 wide, and four lines taller, so the boot at its tallest
# doesn't scroll its head away; the clip keeps the size once it's in
# Ghostty. kit's own lines go no wider than 80; its screens, the splash,
# the arrival and apply's, take the whole window.
#
# 120 columns at 22px are 1560 px, twice the 780 the README shows a frame's
# screen at; with finalize.sh's 40 px margin a frame is 1640, twice 820.

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 22
Set LetterSpacing 0
Set LineHeight 1
# 120 × 34 cells of 13 × 29 px, 1560 × 986: C × 13 + 60 wide, R × 29 + 44 tall.
Set Width 1620
Set Height 1030
Set Padding 16
Set CursorBlink false
Set Framerate 30
Set TypingSpeed 45ms
# Ghostty's theme, its own values (Ghostty.app's Resources/ghostty/themes),
# one line, the one not used commented out: Nord Wave, or Nordfox, which
# kit's look was drawn against. The cursor takes the background, so stills
# show no block.
Set Theme { "name": "Nord Wave", "background": "#212121", "foreground": "#D8DEE9", "cursor": "#212121", "selection": "#D8DEE9", "black": "#3B4252", "red": "#BF616A", "green": "#A3BE8C", "yellow": "#EBCB8B", "blue": "#81A1C1", "magenta": "#B48EAD", "cyan": "#88C0D0", "white": "#E5E9F0", "brightBlack": "#4C566A", "brightRed": "#BF616A", "brightGreen": "#A3BE8C", "brightYellow": "#EBCB8B", "brightBlue": "#81A1C1", "brightMagenta": "#B48EAD", "brightCyan": "#8FBCBB", "brightWhite": "#ECEFF4" }
# Set Theme { "name": "Nordfox", "background": "#2E3440", "foreground": "#CDCECF", "cursor": "#2E3440", "selection": "#3E4A5B", "black": "#3B4252", "red": "#BF616A", "green": "#A3BE8C", "yellow": "#EBCB8B", "blue": "#81A1C1", "magenta": "#B48EAD", "cyan": "#88C0D0", "white": "#E5E9F0", "brightBlack": "#53648D", "brightRed": "#D06F79", "brightGreen": "#B1D196", "brightYellow": "#F0D399", "brightBlue": "#8CAFD2", "brightMagenta": "#C895BF", "brightCyan": "#93CCDC", "brightWhite": "#E7ECF4" }
# The prompt, as Wait looks for it on the cursor's line.
Set WaitPattern /❯/
Set WaitTimeout 1m
