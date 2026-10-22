# Lan Copy

[简体中文](README.md) | English

> One executable. Instant file sharing across your entire local network. No install, no login, no cloud.

Lan Copy is a tiny, zero-dependency LAN file-sharing tool. Run it on one computer and every phone, tablet, or laptop on the same Wi-Fi / LAN can share files and text right from the browser.

## When to use it

- **Phone ↔ Computer**: photos, videos, installers — just drag and drop. No cables.
- **Family & coworkers**: transfer large files within the same network. No compression, no throttling, no third party.
- **Quick text sharing**: links, verification codes, commands, temporary notes — paste it and the other side copies with one tap.

## Quick Start

1. **Download**: get the Windows amd64 or macOS Apple Silicon archive from [Releases](https://github.com/jachy-h/lan-copy/releases/latest) and extract it
2. **Start and open**:
   - Windows: double-click `lan-copy.exe`; the app opens your browser when it starts
   - macOS: open Terminal, change to the extracted folder, and run:

     ```bash
     cd /path/to/Lan-Copy-macOS-arm64
     ./lan-copy
     ```

     Then open <http://localhost:8080> in your browser
3. **Use from other devices**:
   - On this computer, open <http://localhost:8080>
   - Other devices on the same Wi-Fi: open the LAN address shown in the console (or scan the QR code on the page)
4. **Stop**: click "关闭软件" (Shut Down) at the top-right of the web page

On Windows, the console window only shows startup information. Press any key to close it while the service keeps running. Double-clicking the exe again opens the existing page without starting another server.

> Can't reach it? Allow `lan-copy` through your firewall and make sure both devices are on the same LAN (guest Wi-Fi usually isolates clients).

Prefer source? With Go 1.22+ installed:

```bash
git clone https://github.com/jachy-h/lan-copy.git
cd lan-copy
go run .
```

The default storage folder is `共享文件夹-lancopy` on Chinese systems and `shared-folder-lancopy` otherwise. Existing `lan-copy-data` is migrated when the new folder does not exist. Use `-dir` to choose a custom location.

## Highlights

- **Single file**: one Go binary with all web assets embedded — copy it anywhere and it works
- **Zero client**: any device with a browser
- **Live sync**: every open page refreshes instantly when files are uploaded, moved, renamed, or deleted. File and text lists also refresh every minute; hover over or click the online badge to see the last successful data refresh time
- **Shared space**: the default home page for folders, folder creation, moving (including drag and drop), renaming, recursive search, downloads, and dropping files into the current folder; folder deletion requires an empty folder, and moves never overwrite existing entries
- **Browser previews**: Image files show list thumbnails. Click a filename to view common images (including SVG), play browser-supported video/audio codecs, page and zoom through PDFs, or read text and source files. Text previews show the first 256 KiB with UTF-8, GBK and UTF-16 encoding options. Unsupported formats show a message without navigating away and remain downloadable, including Office documents. PDF.js is bundled locally; no Internet connection or extra installation is required.
- **Large files**: streaming uploads never buffer whole files in memory (10 GiB default limit per batch)
- **QR code**: reachable LAN addresses detected automatically, QR ready to scan
- **Local actions**: on the server machine, open files or their folders straight from the web page
- **No overwrites**: duplicates saved as `file (2).ext` automatically
- **Mobile friendly**: safe-area, touch-target, and narrow-screen adaptations
- **No external dependencies**: Go standard library backend, no database; PDF.js is embedded for offline previews

Folder uploads preserve nested paths and empty directories when dragged from a supported desktop browser. The “上传文件夹” button also supports directory selection; browser directory pickers may omit empty directories. Existing top-level names are automatically renamed, never merged or overwritten. Uploads are staged before becoming visible. Each request supports up to 10,000 entries and 64 path components, within the configured size limit.

## Update from the browser

Open Lan Copy through localhost or a LAN address, click the gear to the left of Share, choose **更新** (Update), and drop a matching `.zip` or `.tar.gz` release package into the upload area. The app validates it, replaces the executable, restarts, and reloads the page. Finish other transfers first. Shared files, text, launch arguments, and the working directory are preserved. Failed startup checks trigger an attempt to restore and restart the previous executable; see `lan-copy-update.log` beside the program.

Other LAN devices can also upload signed update packages. `go run` and Docker images disable browser updates; update Docker images through your container workflow. Packages need the new `update.json` manifest and `update.sig` signature, so older release archives are unsupported. Every update requires an Ed25519 signature verified against the built-in trusted public key. Missing or invalid keys disable updates. Release builds require the public key and signing private key; see [UPDATING.md](docs/UPDATING.md). This is an offline upload flow and does not check or download releases automatically.

## Documentation

- **Developer guide** (build, test, architecture, release): [docs/DEVELOPING.md](docs/DEVELOPING.md)
- **Windows warnings and signed releases**: [docs/WINDOWS-SECURITY.md](docs/WINDOWS-SECURITY.md)
- The Windows archive includes `使用说明.html`, an offline service checker. If the service is stopped, double-click `lan-copy.exe` in the same folder.

## Security Note

Lan Copy is designed for trusted home networks and has no accounts or passwords by default. Anyone on the LAN can view, copy, delete shared content, and shut the service down from the web page. Do not expose it to the public internet; be careful on office, campus, or public Wi-Fi.
