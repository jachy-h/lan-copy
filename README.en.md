# Lan Copy

[简体中文](README.md) | English

> One executable. Instant file sharing across your entire local network. No install, no login, no cloud.

Lan Copy is a tiny, zero-dependency LAN file-sharing tool. Run it on one computer and every phone, tablet, or laptop on the same Wi-Fi / LAN can share files and text right from the browser.

## When to use it

- **Phone ↔ Computer**: photos, videos, installers — just drag and drop. No cables.
- **Family & coworkers**: transfer large files within the same network. No compression, no throttling, no third party.
- **Quick text sharing**: links, verification codes, commands, temporary notes — paste it and the other side copies with one tap.

## Quick Start

1. **Download**: grab the archive for your platform from [Releases](https://github.com/jachy-h/lan-copy/releases/latest) and extract it (Windows / macOS, x64 and Apple Silicon supported)
2. **Run**:
   - Windows: double-click `lan-copy.exe`
   - macOS: run `./lan-copy` in a terminal
3. **Use**:
   - Open <http://localhost:8080> on this computer
   - Other devices on the same Wi-Fi: open the LAN address shown in the console (or scan the QR code on the page)
4. **Stop**: click "关闭软件" (Shut Down) at the top-right of the web page

The black console window is informational only — press any key to close it and the service keeps running in the background. Launching it again shows the same screen without occupying a second port.

> Can't reach it? Allow `lan-copy` through your firewall and make sure both devices are on the same LAN (guest Wi-Fi usually isolates clients).

Prefer source? With Go 1.22+ installed:

```bash
git clone https://github.com/jachy-h/lan-copy.git
cd lan-copy
go run .
```

## Highlights

- **Single file**: one Go binary with all web assets embedded — copy it anywhere and it works
- **Zero client**: any device with a browser
- **Live sync**: every open page refreshes instantly when files are uploaded or deleted
- **Large files**: streaming uploads never buffer whole files in memory (10 GiB default limit per batch)
- **QR code**: reachable LAN addresses detected automatically, QR ready to scan
- **Local actions**: on the server machine, open files or their folders straight from the web page
- **No overwrites**: duplicates saved as `file (2).ext` automatically
- **Mobile friendly**: safe-area, touch-target, and narrow-screen adaptations
- **No external dependencies**: Go standard library only, no database

## Documentation

- **Developer guide** (build, test, architecture, release): [docs/DEVELOPING.md](docs/DEVELOPING.md)

## Security Note

Lan Copy is designed for trusted home networks and has no accounts or passwords by default. Anyone on the LAN can view, copy, delete shared content, and shut the service down from the web page. Do not expose it to the public internet; be careful on office, campus, or public Wi-Fi.
