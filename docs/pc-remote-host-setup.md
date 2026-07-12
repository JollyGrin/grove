# Remote grove host: gaming PC (WSL2) setup

Goal: PC becomes the single grove host (workers + orchestrator + cockpit in
tmux, headless); laptop and phone are thin clients over Tailscale SSH.
Design discussion: 2026-07-11 orchestrator chat.

## Where we left off (2026-07-11)

- PC: `DESKTOP-02RIT4K`, Windows 10 22H2, WSL 2.7.10 installed.
- Distro **Ubuntu-20.04 is running WSL1** — that's why systemd never came up.
- `wsl --set-version Ubuntu-20.04 2` failed:
  `HCS_E_HYPERV_NOT_INSTALLED` — **virtualization is not enabled**.
- Already done inside 20.04 (will need redoing if we go fresh 24.04):
  `/etc/wsl.conf` with `[boot] systemd=true`, tailscale package installed.

**⛔ BLOCKED ON: BIOS virtualization (needs physical reboot at the PC).**

## Step 1 — check if BIOS is even needed

Task Manager → Performance → CPU → "Virtualization" line.

- **Enabled** → skip to Step 3.
- **Disabled** → Step 2.

## Step 2 — BIOS: enable virtualization

1. Reboot, mash **Del** (or F2) to enter BIOS/UEFI.
2. Find the setting:
   - **AMD** board: "SVM Mode" — usually Advanced → CPU Configuration.
   - **Intel** board: "Intel VT-x" / "Intel Virtualization Technology".
3. Enable → Save & Exit → boot to Windows.
4. Re-check Task Manager: Virtualization should now say Enabled.

## Step 3 — Windows component + reboot

PowerShell **as Administrator**:

```powershell
wsl.exe --install --no-distribution   # enables Virtual Machine Platform
```

Then **reboot** (required).

## Step 4 — WSL2 distro

Preferred (clean slate; 20.04 is EOL):

```powershell
wsl --install -d Ubuntu-24.04    # installs as WSL2 by default
```

Or convert the existing one instead:

```powershell
wsl --set-version Ubuntu-20.04 2
wsl -l -v                        # must say VERSION 2
```

## Step 5 — inside WSL: systemd + Tailscale SSH

```bash
sudo tee /etc/wsl.conf <<'EOF'
[boot]
systemd=true
EOF
```

PowerShell: `wsl --shutdown`, wait 10 s, reopen WSL.

```bash
systemctl is-system-running      # expect "running" or "degraded"
curl -fsSL https://tailscale.com/install.sh | sh   # skip if converted 20.04
sudo tailscale up --ssh --hostname=pc-wsl          # open the login URL it prints
sudo apt install tmux
```

## Step 6 — the experience test (from laptop)

```bash
ssh dean@pc-wsl        # first connect may pop a browser re-verify (check mode)
tmux new -s test && htop
```

Close laptop lid → reopen → `ssh pc-wsl` → `tmux attach -t test`.
Everything still running = the whole travel workflow in miniature.
Phone: Tailscale app + Termius/Blink → same ssh + attach.

## Later (only after the experience sells itself)

- **Durability:** Task Scheduler task "At startup":
  `wsl.exe -d Ubuntu-24.04 --exec sleep infinity` (hidden) so WSL survives
  Windows reboots. Power settings: sleep = never. Pause Windows Update
  before travel.
- **Grove stack:** go, gh, git SSH keys, `claude` login (URL flow),
  clone repos, `go install ./cmd/gv`, `gv doctor`.
- **Topology decision (from design chat):** PC = only machine with live
  grove state; laptop = thin client + git-synced local checkouts for hand
  edits (or VS Code Remote-SSH into WSL); phone = `grove-mobile` cockpit.
  Task handoff between machines exists but is an escape hatch:
  push branch → `gv untrack` → `gv adopt` on the other host.
- `free -h` inside WSL — default is half of RAM; raise via
  `%UserProfile%\.wslconfig` `[wsl2] memory=` if needed.
- Consider unparking mobile cockpit v2 (issue #5) once phone access is real.
