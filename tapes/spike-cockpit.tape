# Go/no-go spike: does the tmux-attached grove cockpit render inside vhs?
# `gv` (bare) builds the cockpit on an ISOLATED tmux server and execs
# `tmux attach` — this tape proves that renders inside vhs's ttyd pty.
# Run with:  tapes/run.sh tapes/spike-cockpit.tape

Require tmux
Require go

Output tapes/out/spike-cockpit.gif
Output tapes/out/spike-cockpit.txt

Set Shell "bash"
Set FontSize 13
Set Width 1000
Set Height 600
Set Padding 12
Set TypingSpeed 40ms
Set WaitTimeout 60s

# Off-camera: isolate the world (scratch HOME / state / tmux server).
Hide
Type "source tapes/lib/env.sh"
Enter
Wait+Screen /grove tape env ready/
Type `PS1='❯ '; clear`
Enter
Show

Type "gv"
Sleep 500ms
Enter
Wait+Screen /ACTIVITY/
Sleep 4s
