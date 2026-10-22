package account

// ShellInit is `gv account shell-init` (design §Decision 6): a bash/zsh
// `claude` function the operator evals from ~/.bashrc / ~/.zshrc.
//
//   - A CLAUDE_CONFIG_DIR other than ~/.claude (compared after dropping a
//     trailing slash and resolving symlinks) passes straight through, with
//     any inherited token stripped: accounts never touch another dir.
//   - No gv on PATH runs the login, token stripped.
//   - `gv account token-path` failing (active names a missing account, an
//     unreadable store, a gv too old to know the verb) REFUSES with exit 1
//     — gv's own error is on stderr — never a silent login.
//   - An empty path is the login; an unreadable or empty token refuses.
//   - A token session also gets GROVE_ACCOUNT=<name>, the token file's
//     basename less `.token`, so `gv statusline` records its usage under
//     that account; every login path strips an inherited GROVE_ACCOUNT
//     as it strips the token, so a login is never counted as one.
//
// `command`/`env` bypass the function, so it never recurses. Only the
// token PATH crosses gv's stdout; the shell reads the value itself.
const ShellInit = `claude() {
  if [ -n "$CLAUDE_CONFIG_DIR" ]; then
    local d h
    d=$(cd "$CLAUDE_CONFIG_DIR" 2>/dev/null && pwd -P) || d="${CLAUDE_CONFIG_DIR%/}"
    h=$(cd "$HOME/.claude" 2>/dev/null && pwd -P) || h="$HOME/.claude"
    if [ "$d" != "$h" ]; then
      env -u CLAUDE_CODE_OAUTH_TOKEN -u GROVE_ACCOUNT claude "$@"; return; fi   # work/other dirs: never touch
  fi
  if ! command -v gv >/dev/null 2>&1; then
    env -u CLAUDE_CODE_OAUTH_TOKEN -u GROVE_ACCOUNT claude "$@"; return; fi     # no gv: login
  local p; p=$(gv account token-path) || {
    echo "gv account: token-path failed — refusing (gv account use login runs the login)" >&2; return 1; }
  if [ -z "$p" ]; then env -u CLAUDE_CODE_OAUTH_TOKEN -u GROVE_ACCOUNT claude "$@"; return; fi  # login
  local t; t=$(cat "$p") && [ -n "$t" ] || { echo "gv account: token unreadable" >&2; return 1; }
  local n; n=${p##*/}; n=${n%".token"}
  CLAUDE_CODE_OAUTH_TOKEN="$t" GROVE_ACCOUNT="$n" command claude "$@"
}
`

// ShellInitMarker is what doctor looks for in the operator's shell rc
// files to decide shell-init is installed.
const ShellInitMarker = "gv account shell-init"
