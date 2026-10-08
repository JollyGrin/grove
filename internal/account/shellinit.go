package account

// ShellInit is `gv account shell-init` (design §Decision 6): a bash/zsh
// `claude` function the operator evals from ~/.bashrc. It passes a
// foreign CLAUDE_CONFIG_DIR straight through, runs the login (with any
// inherited token stripped) when no account is active or gv is absent,
// and fails closed on an unreadable or empty token. `command`/`env`
// bypass the function, so it never recurses. Only the token PATH crosses
// gv's stdout; the shell reads the value itself.
const ShellInit = `claude() {
  if [ -n "$CLAUDE_CONFIG_DIR" ] && [ "$CLAUDE_CONFIG_DIR" != "$HOME/.claude" ]; then
    command claude "$@"; return; fi              # work/other dirs: never touch
  local p; p=$(gv account token-path 2>/dev/null)
  if [ -z "$p" ]; then env -u CLAUDE_CODE_OAUTH_TOKEN claude "$@"; return; fi  # login
  local t; t=$(cat "$p") && [ -n "$t" ] || { echo "gv account: token unreadable" >&2; return 1; }
  CLAUDE_CODE_OAUTH_TOKEN="$t" command claude "$@"
}
`

// ShellInitMarker is what doctor looks for in the operator's shell rc
// files to decide shell-init is installed.
const ShellInitMarker = "gv account shell-init"
