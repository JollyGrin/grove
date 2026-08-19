#!/bin/bash
set -e

REPO="JollyGrin/grove"
BINARY_NAME="gv"
INSTALL_DIR="${HOME}/.local/bin"

mkdir -p "$INSTALL_DIR"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
    darwin) OS="darwin" ;;
    linux) OS="linux" ;;
    mingw*|msys*|cygwin*)
        echo "grove needs tmux, so native Windows isn't supported — use WSL"
        echo "(inside WSL this script installs the linux binary)."
        exit 1
        ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "Detected: $OS/$ARCH"

# Get latest release tag
LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$LATEST" ]; then
    echo "Failed to fetch latest release"
    exit 1
fi

echo "Installing $BINARY_NAME $LATEST..."

FILENAME="${BINARY_NAME}_${OS}_${ARCH}"
URL="https://github.com/$REPO/releases/download/$LATEST/$FILENAME"

TMP_FILE=$(mktemp)
curl -fsSL "$URL" -o "$TMP_FILE"

mv "$TMP_FILE" "$INSTALL_DIR/$BINARY_NAME"
chmod +x "$INSTALL_DIR/$BINARY_NAME"

echo "Installed $BINARY_NAME $LATEST to $INSTALL_DIR/$BINARY_NAME"
echo ""

# Check if ~/.local/bin is in PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "Add this to your shell profile (~/.zshrc or ~/.bashrc):"
    echo ""
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
    echo "Then restart your terminal or run: source ~/.zshrc"
    echo ""
fi

echo "Next steps:"
echo "  gv doctor    # preflight: tmux, gh, claude"
echo "  cd ~/projects/your-app && gv init   # 🌱 take root"
