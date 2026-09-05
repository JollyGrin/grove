# Remote grove host: Linux VPS runbook

A second grove host for overflow — a Hetzner-class Ubuntu 24.04 VPS reached
over Tailscale SSH — for the Mac-is-home topology from the remote-overflow
train (#176 `hosts:` + `--host` passthrough, #177 `gv handoff`, #178 one-fleet
view). Design discussion: 2026-08-22 orchestrator chat.

## Topology

The **Mac is home**: it holds the operator's checkouts, the cockpit, and most
of the fleet. The **VPS is overflow**: extra workers when the Mac is full, or
when the lid is about to close. There is **no state sync** between hosts —
each has its own `~/.local/state/grove/` (or workspace `.grove/state`), its
own `~/.claude`, its own tmux server. **GitHub is the only shared layer**:
issues are the backlog, branches + PRs are the work, and every task is
tracked by exactly one host at a time. Ownership moves with
`gv handoff grove-N --to grove-host` (#177), or by hand: worker pushes →
`gv untrack grove-N` here → `gv adopt grove-N --repo <r> --branch <b>` there.

## The box

- **Sizing:** one worker ≈ one `claude` process (≈1 GB RSS, bursty CPU while
  it builds/tests). A 4 vCPU / 8 GB VPS runs 4–5 workers comfortably;
  scale linearly. Disk: one worktree per task plus the Go build cache —
  40 GB is plenty.
- **OS:** Ubuntu 24.04 LTS, stock image.
- **User:** a non-root user with sudo (`adduser dean && usermod -aG sudo dean`).
  Everything below runs as that user; the hooks and `hosts.gv` path in the
  Mac's config.yaml assume its home directory.
- **Linger:** `sudo loginctl enable-linger dean` so user systemd units keep
  running without a login session. Two sidecars need it — `gv chat serve`
  and `gv supervise`, see §Sidecars below.

## Tailscale

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up --ssh --hostname=grove-host     # open the login URL it prints
```

- `--ssh` makes tailscaled the SSH server; access is governed by the
  tailnet ACL, not by keys on the box. With MagicDNS on (tailnet default)
  the host is simply `ssh grove-host`.
- **No public port 22.** Leave the provider firewall closed to everything
  except Tailscale's UDP 41641 (or nothing — DERP relays work through a
  closed inbound firewall); disable or firewall sshd if the image ships it.
- **Funnel** (`tailscale funnel`) is the only sanctioned escape hatch for a
  public URL (e.g. a webhook receiver for a future sidecar). Nothing in
  grove needs it; don't turn it on by default.

## Grove stack

```bash
sudo apt update && sudo apt install -y git tmux build-essential
# Go — use the upstream tarball, the apt package lags
curl -fsSL https://go.dev/dl/go1.24.5.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.profile && . ~/.profile
# gh — GitHub's apt repo
(type -p wget >/dev/null || sudo apt install -y wget) && sudo mkdir -p -m 755 /etc/apt/keyrings \
  && wget -qO- https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo tee /etc/apt/keyrings/githubcli-archive-keyring.gpg >/dev/null \
  && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list >/dev/null \
  && sudo apt update && sudo apt install -y gh
gh auth login            # GitHub.com → SSH → device flow; paste the code in any browser
ssh-keygen -t ed25519 -C grove-host && gh ssh-key add ~/.ssh/id_ed25519.pub -t grove-host
ssh -T git@github.com   # "Hi <user>!" = git over SSH works
curl -fsSL https://claude.ai/install.sh | bash   # Claude Code
claude                   # /login → it prints a URL; open it on the Mac/phone, paste the code back
```

Then the repos and grove itself:

```bash
mkdir -p ~/git && cd ~/git
git clone git@github.com:JollyGrin/grove.git         # same directory NAMES as the Mac's config.yaml
git clone git@github.com:<org>/<repo>.git            # …for every repo you want workable here
cd ~/git/grove && go install ./cmd/gv                # → ~/go/bin/gv (absolute path goes in hosts.gv)
gv hooks install                                     # hooks reference the absolute gv path
gv doctor                                            # everything green before you hand it work
```

Repo **names** in the host's `config.yaml` must match the Mac's — `--repo` is
passed through verbatim and resolved by the host's own config, so
`gv grab grove-N --repo grove --host grove-host` finds `~/git/grove` here
regardless of where the Mac keeps its checkout.

Two rules learned live (2026-08-26 shakedown — LEARNINGS §Remote):

- **Passthrough lands on the GLOBAL layer.** Every `--host` verb runs over
  ssh at the login dir — no workspace marker, so the host's gv resolves
  `~/.config/grove/config.yaml` and the global state dir. That file must
  carry **both** the `repos:` map (paths under this host's home) **and**
  `provider: kind: github` — the provider otherwise defaults to markdown
  and remote `adopt`/`grab` fail (`task not found in .grove/tasks`). The
  global layer stays the landing pad for `grab`/`ls`/`adopt`/`handoff`
  even once the host also carries workspace twins (below): since grove-191
  a global-layer `gv ls` aggregates the registered workspaces too, so the
  two coexist.
- **Burn the first-run dialogs at provisioning time.** The first session
  in a fresh `~/.claude` profile shows the folder-trust dialog and the
  bypass-permissions acceptance — and they eat the adopt/grab kickoff
  prompt, leaving the task stuck in `setup` with an empty input. After
  `claude` login, start claude once with the worker flags from the
  worktrees parent (`cd ~/git && claude --dangerously-skip-permissions`),
  accept both prompts (trust: Enter; bypass: `2` then Enter), and exit.
  If it happens anyway: answer the dialogs in the pane, then re-send the
  instructions with `gv nudge`.

## Workspace twins (for `gv orchestrator new --host`)

`gv orchestrator new --host <host>` (grove-198) starts an orchestrator chat
**on the host, inside the host's twin of the workspace you are standing
in** — its own detached `grove-chat-<label>-<n>` tmux session, so a later
`ssh -t <host> tmux attach` (or the cockpit's attach pane) joins the same
live chat from either end. Only NAMES travel: the workspace label and the
profile name, resolved against the host's own registry and config.

That needs a twin: a workspace on the host **registered under the same
label** as the one on the Mac.

```bash
mkdir -p ~/git/<label> && cd ~/git/<label>      # or the repo/parent you want
gv init --yes --label <label>                   # writes .grove/ AND registers it
gv workspaces                                   # the label must be listed, no ✗
```

- The label must match the Mac's exactly — that string is the whole
  addressing scheme. `gv workspaces` on either side shows it.
- The twin gets its **own** `.grove/config.yaml`: its own `orchestrator:
  claude:` command, its own `model_profiles:`, its own `.grove/state`.
  Nothing is copied from the Mac, and nothing is inherited from this
  host's global `config.yaml` either — the `orchestrator:` block is
  deliberately dropped from the global layer inside a workspace, so a
  machine-specific wrapper there can never become a twin's brain.
- **No twin ⇒ hard error**, never a fall-back to the global layer:
  `no workspace '<label>' on @<host> — register a twin there or spawn
  locally`. Same for a registered root whose `.grove/` has moved away, and
  for a `--profile` name the host has no `model_profiles:` entry for.
- Registering workspaces here does **not** disturb passthrough: `--host`
  verbs still land on the global layer (above), and a global-layer `gv ls`
  aggregates the twins' fleets since grove-191.
- The chat is a detached session of its own, not a window in the host's
  cockpit — an ssh client attaching to it cannot resize the cockpit's
  shared windows, and the chat survives the ssh connection dropping.
- A chat can dismiss itself (grove-199): `gv orchestrator close` from
  inside a `grove-chat-*` session kills its pane — and with it the
  session and the claude process — which is what the seeded brain's
  dispatch-and-dismiss instruction assumes. A real cockpit dashboard pane
  is still protected: whether a session is a cockpit is answered by the
  workspace registry, not by the name, because a workspace labelled
  `chat-app` owns the session `grove-chat-app` — the same shape a chat
  produces. An ambiguous name is treated as a cockpit, so such a chat
  must be closed by hand (`C-b :kill-session`, or `tmux kill-session -t
  =<session>`); avoid `chat-`-prefixed workspace labels if you want the
  self-close.
- Because chats sit outside `grove-<label>`, **`gv park` does not stop
  them** (grove-203) — a parked workspace can still have claude processes
  burning on the host. Park says so, one line per survivor with its pid
  and attach hint, and records the names in the `workspace_parked` event;
  `gv audit` lists them any time (report-only, under CHAT SESSIONS, and in
  `--json` as `chat_sessions`). `gv park --chats` is the explicit reap.
  The registry tie-break applies here too: a workspace labelled
  `chat-app` is a COCKPIT, so `--chats` never touches it.

### From the cockpit: `@` (grove-199)

The same spawn, one keypress deep, without leaving the local cockpit:

| key | effect |
| --- | --- |
| `@` | arm a remote spawn; the footer becomes `@<host> ▸ 0 chat · 1-8 profile · ) picker · esc cancel` |
| `@` again | cycle to the next configured host |
| `0` / `O` | spawn on the host's own Claude |
| `1`–`8` | spawn on the profile bound to that digit (the same binding the local digits use — only the NAME is sent, and the host resolves it against its own config) |
| `)` | the profile picker, banner-marked `@<host>`; enter spawns there |
| esc / anything else | cancel |

On success a local pane opens running `ssh -t <host> tmux attach -t
'=grove-chat-<label>-<n>'`, tiled beside the local chats: titled `@<host> ·
<profile>` with its own border color, so remote and local chats are
distinguishable at a glance. On failure — no twin, unknown profile, dead
ssh — the host's own error line becomes the cockpit flash and **no pane is
spawned**.

That pane is nested tmux: your outer prefix (`C-b`) is eaten by the
cockpit's own tmux, so to reach the chat's tmux send the prefix twice
(`C-b C-b`). Same accepted tradeoff as attaching a worker.

Updating the host later: `gv update` is a GitHub-releases self-updater
and errors on a private source install — use
`ssh <host> 'cd ~/git/grove && git pull --ff-only && go install ./cmd/gv'`.

**After `gv update --yes` on a host, read the brain sweep it prints and
refresh what it names** (grove-236). A new binary carries a new
orchestrator seed, so every workspace on that host is suddenly running an
older brain; the sweep runs only when the update actually replaced the
binary and lists each workspace that is behind with the exact command —
run from that workspace's root, over the same ssh hop:

```bash
ssh <host> 'gv update --yes'                 # the sweep prints at the end
ssh <host> 'gv brains'                       # or read it any time, pure read
ssh <host> 'cd <root> && gv init --only orchestrator-md'
```

The sweep reads the host's OWN `~/.config/grove/registry.yaml`, so
running it over ssh reports that host's workspaces — there is no
cross-host push, and nothing is ever overwritten: the refresh drops
`CLAUDE.md.new` beside the brain for you to diff.

## Sidecars: user systemd units

Two grove processes are long-running and want to survive a closed lid, a
dropped ssh session and a reboot: the phone UI (`gv chat serve`) and the
headless supervisor (`gv supervise`). Both belong in **user** systemd units,
not in a tmux window — a tmux window dies with the tmux server, and nothing
restarts it.

Prerequisites, once:

```bash
sudo loginctl enable-linger dean       # user units run with no login session
mkdir -p ~/.config/systemd/user
```

Two things about the `systemd --user` environment that bite here:

- **It does not read `~/.profile`.** The default `$PATH` is
  `/usr/local/bin:/usr/bin:/bin` (+ `~/.local/bin`) — no `~/go/bin`, no
  `/usr/local/go/bin`. Hence the absolute `%h/go/bin/gv` in every
  `ExecStart`. What gv shells out to (`gh`, `git`, `tmux`) is all in
  `/usr/bin`, so nothing else needs a `PATH=` override.
- **`%h` is the unit-file specifier for the user's home** — don't hardcode
  `/home/dean`.

### `gv-chat.service` — the phone UI

`~/.config/systemd/user/gv-chat.service`:

```ini
[Unit]
Description=grove chat UI (gv chat serve)
After=network-online.target

[Service]
Type=simple
ExecStart=%h/go/bin/gv chat serve --port 3000
WorkingDirectory=%h
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now gv-chat.service
tailscale serve --bg 3000        # the entire auth story — see README §Chats from a phone
```

`gv chat serve` is registry-driven: one process serves every registered
workspace, so `WorkingDirectory=%h` is deliberate — there is no ambient
workspace to pick up, and exactly one of these units per host.

### `gv-supervise.service` — the headless supervisor

`gv supervise` (grove-253) is the transition stream — `pr_ready`,
`pr_ci_failed`, `pr_conflicting`, `pr_merged`, `worker_waiting`,
`worker_vanished`, `worker_errored` and the rest — appended to
`events.jsonl` and pushed to the phone. As a unit it runs with **zero
orchestrator chats and zero cockpits alive**, which is the whole point on a
VPS: workers keep going after you close the laptop, and the phone still
hears every transition.

`~/.config/systemd/user/gv-supervise.service`:

```ini
[Unit]
Description=grove supervisor (gv supervise) — grove-repo workspace
After=network-online.target

[Service]
Type=simple
ExecStart=%h/go/bin/gv supervise --interval 30s
WorkingDirectory=%h/git/grove
EnvironmentFile=-%h/.config/grove/.env
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now gv-supervise.service
systemctl --user status gv-supervise          # active (running)
journalctl --user -u gv-supervise -n 20 -f    # transition rows, same format as gv watch
                                              # (silent on a quiet fleet — see below)
```

- **`WorkingDirectory` is the config.** `gv supervise` is ambient-workspace
  scoped and has no `--workspace`/`--host` flag: the cwd decides which
  `.grove/config.yaml` and which `.grove/state` it reads. Point it at the
  registered workspace root (`%h/git/grove` for the `grove-repo` twin) —
  **one unit per supervised workspace**, each with its own file name and its
  own `WorkingDirectory`. A unit left at `%h` supervises the *global* layer
  instead, which is not what you want on a host that carries twins.
- **ntfy is configured in `~/.config/grove/config.yaml`, not in the unit.**
  The push path reads the `notify:` block from the **global** config
  directly (`config.NotifySettings`, and `config.Dir()` is unconditionally
  `~/.config/grove` — it is not workspace-aware), whatever workspace
  supervise is standing in. A `notify:` block in a workspace's
  `.grove/config.yaml` is silently ignored by pushes: no error, just
  silence. Set the topic once, globally:

  ```yaml
  notify:
    ntfy: https://ntfy.sh/<long-random-topic>
    ntfy_body: full        # or title-only, to keep pane text off the ntfy server
  ```

  `EnvironmentFile=-%h/.config/grove/.env` is there for the model-profile
  secrets (`OPENROUTER_API_KEY` and friends), not for ntfy. The leading `-`
  makes the file optional, so the unit still starts clean on a host that
  has none.
- **Config errors beat the lock.** `gv supervise` loads the resolved config
  before it takes the lock, so a `Restart=on-failure` crash-loop right after
  `enable --now` is almost always a config problem, not a supervision one —
  `journalctl --user -u gv-supervise -n 5` prints the exact
  `gv: read config: …` line.
- **`--interval 30s` is the cost-discipline setting.** Each pass is ≤2 tmux
  execs per session, one capture per task and one `gh` round-trip per
  tracked branch. 5s is the recommended floor (not enforced); 30s is right
  for an unattended host.
- **Single emitter — expect the second one to be refused.** The unit holds
  a non-blocking `flock` on `<state>/supervise.lock`, so two processes can
  never double-write `events.jsonl`. Anything else that tries exits 1
  naming the holder:

  ```
  $ gv supervise --once
  gv: already supervised (pid 1540110)
  ```

  That is the lock working, **not a bug**. The cockpit arbitrates the very
  same lock (grove-254): while it is open the cockpit *is* the supervisor,
  driving the engine on the tick it already runs — but only if it wins the
  lock. Open one over ssh while the unit is running and it finds the lock
  taken, renders

  ```
  ⟳ supervised by pid N ·
  ```

  in its header, and **never appends** — the unit stays the single emitter
  and the cockpit just renders the stream it writes. Whoever holds the lock
  emits; the other stays silent. To hand the emitter role back to a desk
  cockpit — or to run a one-shot pass by hand — stop the unit first:

  ```bash
  systemctl --user stop gv-supervise.service
  cd ~/git/grove && gv supervise --once --json
  systemctl --user start gv-supervise.service
  ```
- **A quiet fleet logs nothing.** `gv supervise` prints only when a
  transition fires, so on a host with no open PRs and no waiting worker
  `journalctl --user -u gv-supervise` shows systemd's own `Started …` line
  and then silence — that is healthy, not a hung loop. `systemctl --user
  status` saying `active (running)` is the liveness signal; the one-shot
  above is how you make it say something on demand.

### After every `gv update`

The unit's `ExecStart` path is fixed, so a new binary lands under the
running process without it noticing — the old code keeps running until you
restart it:

```bash
ssh grove-host 'gv update --yes'                        # read the brain sweep it prints
ssh grove-host 'systemctl --user restart gv-chat.service gv-supervise.service'
```

Same rule for both units. (The brain sweep from `gv update --yes` is a
separate follow-up — see the workspace-twins section above.)

## Mac side

Add the host to the Mac's config.yaml (#176):

```yaml
hosts:
  grove-host:
    ssh: dean@grove-host          # Tailscale MagicDNS name
    gv: /home/dean/go/bin/gv      # absolute — same reason the hooks use one
```

First-run test — no manual ssh:

```bash
gv doctor                          # host line: reachable + remote gv version
gv ls --host grove-host            # empty fleet, printed by the host's own gv
gv grab grove-N --repo grove --host grove-host
ssh grove-host -t tmux attach -t '=grove-<label>'   # watch it work; detach with C-b d
gv orchestrator new --host grove-host               # a chat in the host's twin (needs one — see above)
                                                    # prints: ssh -t <host> tmux attach -t '=grove-chat-<label>-<n>'
```

The `=` is tmux's exact-match anchor (so a session whose name merely extends
this one is never attached instead) and the quotes keep it literal: zsh —
macOS's default shell — equals-expands a word that STARTS with `=`, so the
unquoted form dies with `zsh: grove-<label> not found` before `ssh` ever runs
(grove-207). bash has no such expansion; the quoted form is right in both.

## Headless gotchas

- **Do not auto-attach tmux on login** (no `tmux attach` in `.profile`/`.zshrc`):
  every extra client resizes the shared windows and the worker panes with
  them — see the tmux-discipline skill. Attach deliberately, detach when done.
- **Cost reader is per host:** `gv cost` on the Mac only sees workers whose
  transcripts live in the Mac's `~/.claude`; a handed-off task's spend splits
  across hosts.
- **`claude` login is per host:** the token lives in the host's `~/.claude`;
  re-run `claude` → `/login` there if it expires. Nothing syncs it.
- **Reboots are not durable:** tmux is not persistent, so a reboot kills every
  worker session while state still lists them. Recover with `gv audit` →
  the `disconnected` rows → `gv adopt grove-N` for each one you want back.
- **Set the locale** (`sudo update-locale LANG=C.UTF-8`) or tmux renders the
  cockpit glyphs as `?`.

## Phone

Tailscale app + Termius or Blink → `ssh grove-host` → `tmux attach -t
'=grove-<label>'` (or `gv ls`). The `grove-mobile` cockpit (issue #5, parked)
is the intended narrow-screen view once phone access is real.

For a phone that reads and steers without a terminal, and hears every
transition with nothing attached, run the two sidecars above:
`gv-chat.service` (the chat UI over `tailscale serve`) and
`gv-supervise.service` (ntfy pushes for `pr_*` / `worker_*`).

## Appendix — if the host is a Windows PC (WSL2)

The original plan used the gaming PC as host; it stalled on BIOS
virtualization. If you revive it, the stack above applies unchanged inside
the distro; only the platform prep differs:

1. Task Manager → Performance → CPU → "Virtualization" must say Enabled;
   otherwise reboot into BIOS and enable **SVM Mode** (AMD) / **VT-x** (Intel).
2. Admin PowerShell: `wsl.exe --install --no-distribution`, reboot, then
   `wsl --install -d Ubuntu-24.04` (WSL2 by default; `wsl -l -v` must say 2 —
   a WSL1 distro never gets systemd, which Tailscale needs).
3. Inside the distro: `/etc/wsl.conf` with `[boot] systemd=true`, then
   `wsl --shutdown` from PowerShell and reopen; `systemctl is-system-running`.
4. Durability: a Task Scheduler "At startup" task running
   `wsl.exe -d Ubuntu-24.04 --exec sleep infinity` keeps WSL alive across
   Windows logins; set sleep = never and pause Windows Update before travel.
5. Memory: WSL defaults to half of RAM — raise via `%UserProfile%\.wslconfig`
   `[wsl2] memory=` if `free -h` looks small.

Use `--hostname=pc-wsl` (or anything) in `tailscale up`; the Mac's `hosts:`
key is whatever name you pick.
