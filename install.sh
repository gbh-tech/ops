#!/usr/bin/env bash
# ops-cli installer
#
# Usage:
#   curl -fsSL https://ops.gbh.tech/install | bash
#   curl -fsSL https://ops.gbh.tech/install | bash -s -- v1.3.0
#   curl -fsSL https://ops.gbh.tech/install | OPS_VERSION=v1.3.0 bash
#
# Environment overrides:
#   OPS_VERSION      Release tag to install (default: latest)
#   OPS_INSTALL_DIR  Install root (default: $HOME/.ops). Binary is placed in $OPS_INSTALL_DIR/bin
#   OPS_NO_MODIFY_PATH  If set to "1", skip editing shell rc files

set -euo pipefail

REPO="gbh-tech/ops"
INSTALL_DIR="${OPS_INSTALL_DIR:-$HOME/.ops}"
BIN_DIR="$INSTALL_DIR/bin"
VERSION="${1:-${OPS_VERSION:-latest}}"

# Script-scoped so the EXIT trap can still see it after main() returns
# under `set -u`. Initialised empty; populated in main().
TMP_DIR=""
cleanup() { [ -n "$TMP_DIR" ] && rm -rf "$TMP_DIR"; }
trap cleanup EXIT

Red=$'\033[31m'
Green=$'\033[32m'
Yellow=$'\033[33m'
Bold=$'\033[1m'
Reset=$'\033[0m'

info()  { printf '%s==>%s %s\n' "$Bold" "$Reset" "$*"; }
ok()    { printf '%s ok %s %s\n' "$Green" "$Reset" "$*"; }
warn()  { printf '%s warn%s %s\n' "$Yellow" "$Reset" "$*" >&2; }
die()   { printf '%serror%s %s\n' "$Red" "$Reset" "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }

need curl
need tar
need uname
need mkdir
need mv
need chmod
need rm
if command -v shasum >/dev/null 2>&1; then
  SHA_CMD="shasum -a 256"
elif command -v sha256sum >/dev/null 2>&1; then
  SHA_CMD="sha256sum"
else
  die "missing sha256 tool: install either 'shasum' or 'sha256sum'"
fi

detect_platform() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"

  case "$os" in
    Darwin) OS="Darwin" ;;
    Linux)  OS="Linux" ;;
    *) die "unsupported OS: $os (only Darwin and Linux are published)" ;;
  esac

  case "$arch" in
    arm64|aarch64) ARCH="arm64" ;;
    x86_64|amd64)  ARCH="x86_64" ;;
    i386|i686)
      [ "$OS" = "Linux" ] || die "unsupported arch for $OS: $arch"
      ARCH="i386"
      ;;
    *) die "unsupported arch: $arch" ;;
  esac
}

resolve_version() {
  if [ "$VERSION" = "latest" ]; then
    local url
    url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
      "https://github.com/$REPO/releases/latest")"
    VERSION="${url##*/tag/}"
    if [ -z "$VERSION" ] || [ "$VERSION" = "$url" ]; then
      die "could not resolve latest version from GitHub"
    fi
  fi
  case "$VERSION" in
    v*) : ;;
    *)  VERSION="v$VERSION" ;;
  esac
}

download() {
  local url="$1" dest="$2"
  curl -fsSL --retry 3 --retry-delay 1 -o "$dest" "$url" \
    || die "download failed: $url"
}

main() {
  detect_platform
  resolve_version

  local ver_no_v="${VERSION#v}"
  local asset="ops_${OS}_${ARCH}.tar.gz"
  local checksums="ops_${ver_no_v}_checksums.txt"
  local base="https://github.com/$REPO/releases/download/$VERSION"

  info "Installing ops $VERSION for $OS/$ARCH into $BIN_DIR"

  TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t ops-install)"

  info "Downloading $asset"
  download "$base/$asset" "$TMP_DIR/$asset"

  info "Downloading $checksums"
  download "$base/$checksums" "$TMP_DIR/$checksums"

  info "Verifying checksum"
  (
    cd "$TMP_DIR"
    local expected
    expected="$(grep " $asset\$" "$checksums" | awk '{print $1}')"
    [ -n "$expected" ] || die "checksum entry not found for $asset in $checksums"
    printf '%s  %s\n' "$expected" "$asset" | $SHA_CMD -c - >/dev/null \
      || die "checksum verification failed for $asset"
  )
  ok "checksum verified"

  info "Extracting"
  tar -xzf "$TMP_DIR/$asset" -C "$TMP_DIR"
  [ -f "$TMP_DIR/ops" ] || die "binary 'ops' not found in archive"

  mkdir -p "$BIN_DIR"
  mv "$TMP_DIR/ops" "$BIN_DIR/ops"
  chmod +x "$BIN_DIR/ops"
  ok "installed $BIN_DIR/ops"

  update_path

  info "Verifying installation"
  if "$BIN_DIR/ops" --version >/dev/null 2>&1; then
    ok "$("$BIN_DIR/ops" --version 2>&1 | head -n1)"
  else
    warn "binary did not respond to --version; install may be incomplete"
  fi

  printf '\n%sops%s is installed at %s%s%s\n' "$Bold" "$Reset" "$Bold" "$BIN_DIR/ops" "$Reset"
  if ! echo ":$PATH:" | grep -q ":$BIN_DIR:"; then
    printf 'Restart your shell or run: %sexport PATH="%s:$PATH"%s\n' "$Bold" "$BIN_DIR" "$Reset"
  fi
}

# Append a guarded PATH block to the given rc file if not already present.
# $1: rc file path, $2: line to append
append_if_missing() {
  local file="$1" line="$2"
  [ -f "$file" ] || touch "$file"
  if ! grep -Fq "# ops-cli: managed block" "$file" 2>/dev/null; then
    {
      printf '\n# ops-cli: managed block\n'
      printf '%s\n' "$line"
    } >> "$file"
    ok "updated $file"
  fi
}

update_path() {
  if [ "${OPS_NO_MODIFY_PATH:-0}" = "1" ]; then
    return
  fi
  if echo ":$PATH:" | grep -q ":$BIN_DIR:"; then
    return
  fi

  local posix_line="export PATH=\"$BIN_DIR:\$PATH\""
  local fish_line="fish_add_path -g $BIN_DIR"

  case "${SHELL:-}" in
    */fish)
      append_if_missing "$HOME/.config/fish/config.fish" "$fish_line"
      ;;
    */zsh)
      append_if_missing "$HOME/.zshrc" "$posix_line"
      ;;
    */bash|*)
      # Cover both interactive login (.bash_profile on macOS) and non-login (.bashrc on Linux).
      if [ "$(uname -s)" = "Darwin" ]; then
        append_if_missing "$HOME/.bash_profile" "$posix_line"
      else
        append_if_missing "$HOME/.bashrc" "$posix_line"
      fi
      ;;
  esac
}

main "$@"
