#!/bin/bash

set -e

REPO="grapple-solution/grapple-go-cli"
VERSION=${1:-"latest"}

# Detect OS
OS="$(uname | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

# Normalize architecture
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "Detected OS: $OS, Architecture: $ARCH"

# Resolve latest version if needed
if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | cut -d'"' -f4)
fi

TARBALL="grapple-${OS}-${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${TARBALL}"

echo "Downloading: $DOWNLOAD_URL"
curl -L "$DOWNLOAD_URL" -o "$TARBALL"

echo "Extracting $TARBALL..."
tar -xzf "$TARBALL"

FOLDER_NAME="grapple-${OS}-${ARCH}"

echo "Installing grapple CLI to /usr/local/bin..."
sudo mv "${FOLDER_NAME}/grapple" /usr/local/bin/

echo "Installing shared files to /usr/local/share/grapple-go-cli/..."
sudo mkdir -p /usr/local/share/grapple-go-cli
sudo mv "${FOLDER_NAME}/template-files" /usr/local/share/grapple-go-cli/
sudo mv "${FOLDER_NAME}/files" /usr/local/share/grapple-go-cli/

echo "Cleaning up..."
rm -rf "$TARBALL" "$FOLDER_NAME"

echo "✅ Grapple CLI installed!"
echo "Run 'grapple help' to get started."
