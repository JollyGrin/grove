# Sourced by e2e suites that don't need gh (grove-540). install_gh_stub
# <scratch> puts an offline `gh` first on PATH: it records argv to
# <scratch>/gh-stub.log and fails like an unreachable network, so no suite
# ever spends the operator's real token or quota. A suite that needs canned
# answers writes its own stub instead (github.sh).
install_gh_stub() {
  mkdir -p "$1/gh-stub-bin"
  cat > "$1/gh-stub-bin/gh" <<STUB
#!/bin/sh
echo "\$*" >> "$1/gh-stub.log"
echo "gh stub (grove-540): offline" >&2
exit 1
STUB
  chmod +x "$1/gh-stub-bin/gh"
  export PATH="$1/gh-stub-bin:$PATH"
}
