# Appanel

A self-hosted hosting control panel for Ubuntu and Debian servers, in the spirit of cPanel, aaPanel and FlyPanel. One Go binary, no database server, no runtime to install.

> **Status: early development (Phase 1).** The foundation works and is tested; most hosting features are still ahead. See the [roadmap](#roadmap). Don't run this on a production server yet.

## What works today

| Area | State |
| --- | --- |
| Single binary (`appanel`), dashboard embedded | done |
| One-line installer (`install.sh`): checksum-verified download, systemd service, opens ufw port, uninstall | done |
| Random port + secret URL path (everything else returns 404) | done |
| Self-signed HTTPS by default | done |
| Login: PBKDF2-SHA256 passwords, login throttling, CSRF header, strict cookies | done |
| Optional 2FA (TOTP, works with any authenticator app) | done |
| Dashboard: CPU, memory, disk, network, load, uptime, CPU history | done |
| File Manager: browse, edit, upload, download, rename, delete, new folder | done |
| Web terminal: real root shell over WebSocket, colours, history, tab completion, Ctrl+C, resize | done |
| Release pipeline: every push to `main` publishes binaries the installer downloads | done |
| Full-screen terminal programs (vim, nano, htop) | not yet |
| Everything else in the roadmap | planned |

## Install

On a fresh Ubuntu or Debian server, as root:

```sh
curl -fsSL https://raw.githubusercontent.com/usmanawa2153-beep/appanel/main/install.sh | sudo bash
```

The installer picks a random port and secret URL, creates the `admin` account with a random password and prints all three once. Open the URL, accept the self-signed certificate warning, sign in, then change the password and enable 2FA under **Settings**.

Useful commands:

```sh
appanel info              # show the access URL again
appanel reset-password    # print a new random admin password
appanel disable-2fa       # lock-out recovery
systemctl status appanel
```

Installer options (environment variables): `APPANEL_VERSION`, `APPANEL_PORT`, `APPANEL_HOST`, `APPANEL_NO_TLS=1`. Running the installer again upgrades the binary and keeps your login and settings.

Uninstall (keeps your data) or remove everything:

```sh
curl -fsSL https://raw.githubusercontent.com/usmanawa2153-beep/appanel/main/install.sh | sudo bash -s -- --uninstall
curl -fsSL https://raw.githubusercontent.com/usmanawa2153-beep/appanel/main/install.sh | sudo bash -s -- --uninstall --purge
```

Releases are built by GitHub Actions on every push to `main` (version `v0.1.<run number>`), or from a `v*` tag.

## Build from source

Needs Go 1.24 or newer.

```sh
make build         # -> bin/appanel
make test
APPANEL_DATA=./data ./bin/appanel init --listen 127.0.0.1
APPANEL_DATA=./data ./bin/appanel serve
```

## Security model

- The panel only answers under its secret path; scanners hitting the port see a plain 404.
- Passwords are hashed with PBKDF2-HMAC-SHA256 (600k iterations). Five failed attempts lock an IP for 15 minutes; the check uses the socket address, never `X-Forwarded-For`.
- Sessions are HttpOnly, SameSite=Strict, `Secure` over TLS, and live in memory (a restart signs everyone out). Changing the password signs out other devices.
- State-changing requests need a custom CSRF header that browsers cannot send cross-site.
- The UI builds all DOM with `textContent` and ships a strict Content-Security-Policy (no inline script or style).
- The File Manager confines every path to its root, resolves symlinks before checking, writes atomically and never follows a symlink out of the root.
- The terminal WebSocket needs a valid session and a same-origin `Origin` header (blocks cross-site hijacking), and is capped at 5 open terminals.
- The panel runs as root because it has to manage the server. Treat access to it like root SSH access: keep 2FA on and the port firewalled where you can.

Found a vulnerability? Please open a private security advisory on GitHub rather than a public issue.

## Roadmap

1. **Phase 1:** installer, Go agent, auth, dashboard, File Manager, web terminal *(done, apart from full-screen terminal apps)*
2. **Phase 2:** websites on Nginx, Let's Encrypt SSL, DNS provider APIs (Cloudflare and others)
3. **Phase 3:** MySQL/Postgres and a database manager, cron, firewall, backups
4. **Phase 4:** Node/Python/Docker app deploy, process manager, one-click apps
5. **Phase 5:** Telegram/email alerts, AI assistant, REST API + CLI, multi-user accounts

The user model already stores a role per account so multi-user can be added without a data migration. The dashboard is currently dependency-free JavaScript; a React + Vite front end is planned once the API settles.

## Layout

```
cmd/appanel/        CLI entrypoint: init, serve, reset-password, ...
internal/auth/      passwords, TOTP, sessions, rate limiting
internal/config/    config file, random port and secret path
internal/files/     path-confined file operations
internal/server/    HTTP API and security headers
internal/store/     persistence (JSON file today, swappable)
internal/system/    /proc metrics
internal/term/      PTY shell sessions
internal/ws/        WebSocket (RFC 6455) server
internal/tlsutil/   self-signed certificate
web/ui/             embedded dashboard
install.sh          installer for Ubuntu/Debian
```

## License

Appanel is **source-available** under the [Elastic License 2.0](LICENSE). You can use, modify and share it freely, including on servers you run for your own or your clients' sites. You **cannot** offer Appanel itself to others as a hosted or managed service, and you must keep the license and copyright notices intact. Because ELv2 restricts that use, Appanel is not "open source" by the OSI definition, so we call it source-available.
