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

**Printing (Windows host)**: click the printer icon immediately left of Share, choose a printer installed on the host, then select a local PDF, JPG/JPEG, or PNG. Review pages and zoom the preview before confirming, or choose **⋯ → 打印** (Print) on a supported file in Shared Space to open its print preview directly. Clicking the filename still opens the regular preview. You can also send a test page first. Printing uses the host printer’s default paper and orientation, one copy, single-sided. Files are limited to 50 MiB and PDFs to 50 pages. “Submitted” means handed to the Windows queue; check the device for actual output.

**Other devices cannot connect?** Check that they are on the same LAN and allow Lan Copy through the system firewall. Guest Wi-Fi may isolate devices.

**Update the app**: click the gear → **更新** (Update) to check the latest stable GitHub Release. The host user or a guest on the local network can choose **下载并更新** (Download and update) to update the computer running Lan Copy, or **前往查看** (View release). You can also download the package for the host system manually and drop in the complete archive. After signature verification, the app installs the update, restarts, and reloads the page, preserving shared content. Finish other file transfers first.

> Use a trusted local network. There are no accounts or passwords by default: others on the LAN can read, modify, or delete shared content and initiate an app update. Do not expose it to the public internet.

---

**Start at login**: On the computer running Lan Copy, open Settings → 通用 (General) → 开机自启动 (Start at login). Windows and macOS are supported; this is off by default. Changes apply at the next login and launch the service in the background without opening a browser. Quitting does not disable it. Keep the executable and shared folder in place; enable it again after moving them. Allow the app in your system startup/login settings if blocked.

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
make build                   # Build with VERSION as-is; do not auto-increment
make build VERSION=1.2.3     # Build with an explicit version
make publish                 # Build, commit VERSION, tag, and push from clean main
```

Builds use the exact version in `VERSION` (or `0.1.0` if the file is missing) and do not auto-increment it. An explicit `VERSION=x.y.z` overrides the file. After every package builds successfully, the selected version is saved to `VERSION`; a failed build leaves it unchanged. Distribute the matching archives from `release/`. Commit `VERSION` with release changes; `.config/` and `release/` are ignored by Git.

`make publish` requires all code changes to be committed first and asks you to confirm the target version with `y` (`n` cancels). It increments the version, commits only `VERSION`, creates the release tag, and pushes to `origin` so Actions can publish the release.

Pushing `main` runs tests and builds signed packages in GitHub Actions. Pushing a `vVERSION` tag matching the committed `VERSION` publishes a GitHub Release. Configure repository signing credentials first; see [automated builds and releases](docs/GITHUB-ACTIONS.md).

- [Developer guide](docs/DEVELOPING.md): architecture, command-line options, tests, Docker, and cross-platform builds.
- [Update release guide](docs/UPDATING.md): signing keys, versioning, package format, and recovery.
- [Windows security and signed releases](docs/WINDOWS-SECURITY.md): system warnings and Windows code signing.
