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
  running without a login session — nothing needs it today, but the
  planned sidecars (Telegram, watchers) ship as user units.

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

- **The host is GLOBAL-layer only.** Every `--host` verb runs over ssh at
  the login dir — no workspace marker, so the host's gv resolves
  `~/.config/grove/config.yaml` and the global state dir. That file must
  carry **both** the `repos:` map (paths under this host's home) **and**
  `provider: kind: github` — the provider otherwise defaults to markdown
  and remote `adopt`/`grab` fail (`task not found in .grove/tasks`). Do
  NOT mirror the Mac's workspace `.grove/config.yaml` layout here; it is
  invisible to passthrough.
- **Burn the first-run dialogs at provisioning time.** The first session
  in a fresh `~/.claude` profile shows the folder-trust dialog and the
  bypass-permissions acceptance — and they eat the adopt/grab kickoff
  prompt, leaving the task stuck in `setup` with an empty input. After
  `claude` login, start claude once with the worker flags from the
  worktrees parent (`cd ~/git && claude --dangerously-skip-permissions`),
  accept both prompts (trust: Enter; bypass: `2` then Enter), and exit.
  If it happens anyway: answer the dialogs in the pane, then re-send the
  instructions with `gv nudge`.

Updating the host later: `gv update` is a GitHub-releases self-updater
and errors on a private source install — use
`ssh <host> 'cd ~/git/grove && git pull --ff-only && go install ./cmd/gv'`.

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
ssh grove-host -t tmux attach -t =grove-<label>     # watch it work; detach with C-b d
```

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
=grove-<label>` (or `gv ls`). The `grove-mobile` cockpit (issue #5, parked)
is the intended narrow-screen view once phone access is real; a Telegram
sidecar (read-only fleet digest + `gv answer` relay, as a user systemd unit —
hence `enable-linger`) is the next idea after that.

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
