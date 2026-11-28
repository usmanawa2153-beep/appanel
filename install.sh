#!/usr/bin/env bash
# Appanel installer for Ubuntu and Debian.
#
#   curl -fsSL https://raw.githubusercontent.com/usmanawa2153-beep/appanel/main/install.sh | sudo bash
#
# Environment:
#   APPANEL_VERSION   release tag to install (default: latest)
#   APPANEL_PORT      fixed panel port (default: random free port)
#   APPANEL_HOST      public IP or domain for the printed URL (default: auto-detected)
#   APPANEL_BINARY    install this local binary instead of downloading (for testing)
#   APPANEL_NO_TLS=1  serve plain HTTP (only if a reverse proxy terminates TLS)
#
# Everything runs inside main() so a truncated download can never execute half a script.
set -euo pipefail

REPO="usmanawa2153-beep/appanel"
BIN="/usr/local/bin/appanel"
DATA_DIR="/var/lib/appanel"
UNIT="/etc/systemd/system/appanel.service"

TMP_DIR=""
cleanup() { [ -n "${TMP_DIR:-}" ] && rm -rf "$TMP_DIR"; return 0; }
trap cleanup EXIT

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

check_system() {
  [ "$(id -u)" -eq 0 ] || fail "run as root (use sudo)"
  [ "$(uname -s)" = "Linux" ] || fail "Linux only"
  [ -r /etc/os-release ] || fail "cannot detect the operating system"
  # shellcheck disable=SC1091
  . /etc/os-release
  case "${ID:-}" in
    ubuntu|debian) ;;
    *) case " ${ID_LIKE:-} " in
         *" debian "*|*" ubuntu "*) ;;
         *) fail "Appanel V1 supports Ubuntu and Debian only (found: ${PRETTY_NAME:-unknown})" ;;
       esac ;;
  esac
  command -v systemctl >/dev/null 2>&1 || fail "systemd is required"
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) fail "unsupported CPU architecture: $(uname -m)" ;;
  esac
}

ensure_tools() {
  local missing=()
  for t in curl tar sha256sum; do command -v "$t" >/dev/null 2>&1 || missing+=("$t"); done
  if [ "${#missing[@]}" -gt 0 ]; then
    say "installing: ${missing[*]}"
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl tar coreutils ca-certificates
  fi
}

download_binary() {
  local arch="$1" version="${APPANEL_VERSION:-latest}" base tmp asset
  asset="appanel-linux-${arch}.tar.gz"
  if [ "$version" = "latest" ]; then
    base="https://github.com/${REPO}/releases/latest/download"
  else
    base="https://github.com/${REPO}/releases/download/${version}"
  fi
  tmp="$(mktemp -d)"
  TMP_DIR="$tmp"
  say "downloading ${asset} (${version})"
  curl -fsSL --retry 3 -o "${tmp}/${asset}" "${base}/${asset}" \
    || fail "download failed. Is there a published release at https://github.com/${REPO}/releases ?"
  curl -fsSL --retry 3 -o "${tmp}/checksums.txt" "${base}/checksums.txt" || fail "could not download checksums.txt"
  say "verifying checksum"
  ( cd "$tmp" && grep " ${asset}\$" checksums.txt | sha256sum -c - >/dev/null ) \
    || fail "checksum mismatch, refusing to install"
  tar -xzf "${tmp}/${asset}" -C "$tmp" appanel
  install -m 0755 "${tmp}/appanel" "$BIN"
}

install_local_binary() {
  [ -f "$APPANEL_BINARY" ] || fail "APPANEL_BINARY=$APPANEL_BINARY not found"
  say "installing local binary $APPANEL_BINARY"
  install -m 0755 "$APPANEL_BINARY" "$BIN"
}

write_unit() {
  cat >"$UNIT" <<'EOF'
[Unit]
Description=Appanel hosting control panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=APPANEL_DATA=/var/lib/appanel
ExecStart=/usr/local/bin/appanel serve
Restart=on-failure
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$UNIT"
}

main() {
  check_system
  local arch; arch="$(detect_arch)"
  ensure_tools

  if [ -n "${APPANEL_BINARY:-}" ]; then install_local_binary; else download_binary "$arch"; fi

  say "initialising"
  local args=()
  [ -n "${APPANEL_PORT:-}" ] && args+=(--port "$APPANEL_PORT")
  [ "${APPANEL_NO_TLS:-0}" = "1" ] && args+=(--no-tls)
  local ip
  ip="${APPANEL_HOST:-$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)}"
  [ -n "$ip" ] || ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  [ -n "$ip" ] && args+=(--host "$ip")

  mkdir -p "$DATA_DIR" && chmod 700 "$DATA_DIR"
  local first_run=1
  [ -f "$DATA_DIR/config.json" ] && first_run=0
  APPANEL_DATA="$DATA_DIR" "$BIN" init "${args[@]}"

  write_unit
  systemctl daemon-reload
  systemctl enable appanel >/dev/null 2>&1
  # restart (not just start): on an upgrade the old binary is still running.
  systemctl restart appanel >/dev/null 2>&1 || true
  sleep 2
  systemctl is-active --quiet appanel || fail "service failed to start; see: journalctl -u appanel -n 50"

  open_firewall

  if [ "$first_run" -eq 0 ]; then
    say "Appanel upgraded and running. Your existing login is unchanged."
  else
    say "Appanel is running."
  fi
  echo
  echo "Can't open the URL? Your hosting provider's own firewall (security group) must allow the"
  echo "panel port shown above (TCP). Many clouds block unknown ports by default."
  echo "Update later from the panel (Settings -> Updates) or by running this installer again."
  echo "Show the URL again any time:  APPANEL_DATA=$DATA_DIR appanel info"
  echo "Uninstall:                   curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | sudo bash -s -- --uninstall"
}

# If ufw is active, allow the panel's port so the URL works straight away.
open_firewall() {
  command -v ufw >/dev/null 2>&1 || return 0
  ufw status 2>/dev/null | grep -qi "^Status: active" || return 0
  # With a panel domain the panel is loopback-only behind Nginx; its port stays closed.
  grep -q '"panel_domain"' "$DATA_DIR/config.json" 2>/dev/null && return 0
  local port
  port="$(APPANEL_DATA="$DATA_DIR" "$BIN" info --port 2>/dev/null || true)"
  [ -n "$port" ] || return 0
  say "ufw is active: allowing ${port}/tcp"
  ufw allow "${port}/tcp" >/dev/null
}

uninstall() {
  [ "$(id -u)" -eq 0 ] || fail "run as root (use sudo)"
  say "stopping and removing the service"
  systemctl disable --now appanel >/dev/null 2>&1 || true
  rm -f "$UNIT" "$BIN"
  systemctl daemon-reload >/dev/null 2>&1 || true
  if [ "${2:-}" = "--purge" ]; then
    rm -rf "$DATA_DIR"
    say "removed Appanel and all its data"
  else
    say "removed Appanel. Your data is kept in $DATA_DIR (re-run with --uninstall --purge to delete it)."
  fi
}

case "${1:-}" in
  --uninstall) uninstall "$@" ;;
  "")          main ;;
  *)           fail "unknown option: $1 (supported: --uninstall [--purge])" ;;
esac
