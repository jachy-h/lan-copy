# Lan Copy

[简体中文](README.md) | English

Run Lan Copy on one computer to share files and text with phones, tablets, and computers on the same local network. Use a browser; no login or cloud storage needed.

## When to use it

- Transfer photos, videos, and files between a phone and a computer.
- Share folders or large files on the same network.
- Copy links, text, and temporary notes across devices.

## Get started

1. Download and extract the archive for your system from [Releases](https://github.com/jachy-h/lan-copy/releases/latest). Packages are available for Windows x64 and macOS Apple Silicon.
2. Start the app:
   - **Windows**: double-click `lan-copy.exe`. Your browser opens automatically.
   - **macOS**: in Terminal, change to the extracted folder and run `./lan-copy`. Then open <http://localhost:8080>.
3. Click the **Share icon** at the top of the page. Other devices on the same Wi-Fi / LAN can scan the QR code or open the LAN address in their browser.
4. Drop files or folders into **共享空间** (Shared Space). Publish and copy text in **文本快传** (Text Transfer).

When finished, click **关闭软件** (Shut Down) at the top right. On Windows, pressing any key closes the console while the service keeps running.

Files and text stay on the computer running Lan Copy. The default shared folder is named `共享文件夹-lancopy` on Chinese systems and `shared-folder-lancopy` otherwise.

**Other devices cannot connect?** Check that they are on the same LAN and allow Lan Copy through the system firewall. Guest Wi-Fi may isolate devices.

**Update the app**: download a newer release package for your system, click the gear → **更新** (Update), and drop in the complete archive. After signature verification, the app installs the update, restarts, and reloads the page, preserving shared content. Finish other file transfers first.

> Use a trusted local network. There are no accounts or passwords by default: others on the LAN can read, modify, or delete shared content and shut down the service. Do not expose it to the public internet.

---

## For developers

**Regular users can ignore everything below. Release packages work without installing Go, building the app, or generating signing keys.**

### Local development

Requires Go 1.22 or later.

```bash
git clone https://github.com/jachy-h/lan-copy.git
cd lan-copy
go run .
```

Open <http://localhost:8080>. Browser updates are disabled when running from source.

### Build and release

Requires Go, Make, and Python 3. Before the first build, follow the [update release guide](docs/UPDATING.md) to create `.config/update.key` and `.config/update.pub`. Reuse the same keys for subsequent releases; never commit or distribute the private key.

```bash
make test                    # Run build-script and Go tests
make build                   # Sign packages, bump patch, and print build details
make build VERSION=1.2.3     # Or choose an explicit version
```

Builds read the baseline from `VERSION` and save the new version only after all packages succeed. Distribute the matching archives from `release/`. Commit `VERSION` with release changes; `.config/` and `release/` are ignored by Git.

- [Developer guide](docs/DEVELOPING.md): architecture, command-line options, tests, Docker, and cross-platform builds.
- [Update release guide](docs/UPDATING.md): signing keys, versioning, package format, and recovery.
- [Windows security and signed releases](docs/WINDOWS-SECURITY.md): system warnings and Windows code signing.
