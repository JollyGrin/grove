# Scenario: a worker asked a question — see it in the cockpit, answer it
# from the dashboard, watch the answer land in the ACTIVITY feed.
# Fixture: tapes/lib/env.sh seeds task-102 waiting on a question (floats to
# the top of AGENTS, selected by default) plus one working + one done task,
# and a stub worker window so the answer paste has somewhere to go.
# Run with:  tapes/run.sh tapes/cockpit-question.tape

Require tmux
Require go

Output tapes/out/cockpit-question.gif
Output tapes/out/cockpit-question.mp4
Output tapes/out/cockpit-question.txt
Output tapes/out/cockpit-question.ascii

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

# Launch the cockpit: dashboard left, orchestrator chat right.
Type "gv"
Sleep 500ms
Enter
Wait+Screen /ACTIVITY/
Sleep 2s

# The cockpit lands focused on the orchestrator pane — hop to the dash.
Ctrl+B
Type "o"
Sleep 1s

# task-102's question is the selected row; enter opens the detail view.
Enter
Wait+Screen /ANSWER/
Sleep 1s

Type "retry twice, then fail the build and page me"
Sleep 500ms
Enter

# The answer relays to the worker pane; the feed records it.
Wait+Screen /answered/
Sleep 4s
