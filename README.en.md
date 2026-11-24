# Lan Copy

[简体中文](README.md) | English

Run Lan Copy on one computer to share files and text with phones, tablets, and computers on the same local network. Use a browser; no login or cloud storage needed.

## When to use it

- Transfer photos, videos, and files between a phone and a computer.
- Share folders or large files on the same network.
- Copy links, text, and temporary notes across devices.

## Get started

**macOS (Apple Silicon) can install with Homebrew** instead of extracting manually:

```bash
brew install jachy-h/jachy-h/lancopy
lancopy                       # prints startup logs, runs in the background, then open http://localhost:8080
```

`lancopy` prints the local and LAN addresses, moves the server to the background, and returns your prompt. Running it again reports that Lan Copy is already running. Logs go to `~/Library/Logs/lancopy/lancopy.log`; set `LAN_COPY_LOG_FILE` to use another path. Stop the service with **关闭软件** (Shut Down) at the top right of the page, or use `brew services start lancopy` when you want launchd to keep it alive. Passing flags (for example `lancopy -dir ~/shared`) keeps it in the foreground; press `Ctrl-C` to stop.

A Homebrew install provides the `lancopy` command; upgrade it with `brew upgrade lancopy`. The in-app updater rewrites files owned by Homebrew and should be avoided. Intel Macs are not supported yet.

1. Download and extract the archive for your system from [Releases](https://github.com/jachy-h/lan-copy/releases/latest). Packages are available for Windows x64 and macOS Apple Silicon.
2. Start the app:
   - **Windows**: double-click `lan-copy.exe`. Your browser opens automatically.
   - **macOS**: in Terminal, change to the extracted folder and run `./lan-copy` (runs in the foreground; press `Ctrl-C` to stop). Then open <http://localhost:8080>.
3. Click the **Share icon** at the top of the page. Other devices on the same Wi-Fi / LAN can scan the QR code or open the LAN address in their browser.
4. Drop files or folders into **内网空间** (Shared Space). In **碎片快传** (Fragment Transfer), publish text or paste, drag and drop images, and save a fragment into any folder in Shared Space. Fragments expire after 7 days by default; change this under **Settings → General**.

When finished, click **关闭软件** (Shut Down) at the top right. On Windows, pressing any key closes the console while the service keeps running.

Files and text stay on the computer running Lan Copy. The default shared folder is named `共享文件夹-lancopy` on Chinese systems and `shared-folder-lancopy` otherwise.

**Printing (Windows host)**: click the printer icon immediately left of Share, choose a printer installed on the host, then select a local PDF, JPG/JPEG, or PNG. Review pages and zoom the preview before confirming, or choose **⋯ → 打印** (Print) on a supported file in Shared Space to open its print preview directly. Clicking the filename still opens the regular preview. You can also send a test page first. Printing uses the host printer’s default paper and orientation, one copy, single-sided. Files are limited to 50 MiB and PDFs to 50 pages. “Submitted” means handed to the Windows queue; check the device for actual output.

**Smart tables**: CSV files in the shared folder stay ordinary files by default. Clicking a CSV/TSV file name opens a read-only table preview with the first row as the header, and the toolbar switches between the table and the raw text. To edit, query, or expose a table to plugins, mark it as a smart table from its file-row menu to show the **表格** badge; open it to browse, search, sort, and edit. The search box queries every column as you type and highlights matches. The table toolbar's **列设置** (column settings) panel reorders columns and hides or shows individual columns; preferences live in `<table-file-name>.hidden.metadata.json` next to the CSV. They affect only the browser view, never the CSV, plugin column order, or table version, and the metadata file is omitted from file listings, search, downloads, and previews. Plugins use `LAN_COPY_API_URL`/`LAN_COPY_API_TOKEN` to query and call `lock_and_update`; content versions prevent silent overwrites.

**Plugin services (LAN-accessible)**: open **Settings → 插件 (Plugins)** and choose **在新标签页打开插件服务** to import `docker save` / `podman save` archives, install, start, stop, and inspect local plugins through Docker or Podman. Plugins run in restricted containers, but the service is reachable from the LAN; any device that can reach Lan Copy can manage plugins, so install only trusted images. Lan Copy detects an available Docker or Podman runtime at startup (Docker first), and the plugins page can start Podman when it is unavailable; one of them must be installed on the host. Manifests can subscribe to lifecycle events for uploads, file/folder changes, and shared-text changes; the **对接** (integration) menu documents the events, shows delivery status, can send a test event, and explains the smart-table plugin API. A manifest that declares `ui` also gets a LAN-accessible browser entry at `http://<lan-copy>/plugin-app/<plugin-id>/`; plugins should honor `X-Forwarded-Prefix` to work under that subpath. Plugins that need a post-upload confirmation page can declare `uploadHook`: after a committed upload Lan Copy calls the hook and shows the returned page in a same-origin modal; hook failures or timeouts never fail the upload. See the [plugin protocol](docs/PLUGIN-PROTOCOL.md) for the manifest, image build rules, and security boundaries.

**Other devices cannot connect?** Check that they are on the same LAN and allow Lan Copy through the system firewall. Guest Wi-Fi may isolate devices.

**A plugin cannot reach Lan Copy?** On Windows/macOS, Podman runs inside a `podman machine` VM, where `host.containers.internal` points at the VM rather than the host. Lan Copy automatically uses a host LAN IPv4 instead and shows the read-only **Plugin API address** and the reason at the top of **Settings → Plugins**. If the address is wrong, allow the listening port through the host firewall, or pass `-plugin-api-url http://<reachable-address>:<port>`. Restart the plugin (this recreates its container) or reinstall it so the new address is injected. Linux-native Podman and Docker are unchanged.

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

For development, run `air` at the repository root and `make frontend-dev` in another terminal, then open the URL printed by Vite. The repository `.air.toml` passes `-air-dev`, which replaces the backend `/` page with frontend startup instructions. APIs, vendor files, and logos remain available to the Vite proxy. Regular `go run .`, `go build .`, and release artifacts continue to serve the embedded frontend.


### Build and release

Requires Go, Make, and Python 3. Before the first build, follow the [update release guide](docs/UPDATING.md) to create `.config/update.key` and `.config/update.pub`. Reuse the same keys for subsequent releases; never commit or distribute the private key.

```bash
make test                             # Run build-script and Go tests
make build                            # Increment the build: 0.8.1-build3 → 0.8.1-build4
make build VERSION=0.8.2              # Start a new base version at 0.8.2-build1
make publish                          # Increment the build, commit VERSION, tag, and push from clean main
```

Versions use `x.y.z-build{c}`; for example, `0.8.1-build3` is the third build of `0.8.1`, and a missing file starts at `0.1.0-build1`. Each successful `make build` increments the build number and saves it to `VERSION`; a failed build leaves it unchanged. Raising the base version (for example `0.8.1` → `0.8.2`) resets the counter, so the first build is `0.8.2-build1`. An explicit `VERSION=x.y.z-build{c}` is used exactly as given, which CI and rebuilds rely on. Distribute the matching archives from `release/`. Commit `VERSION` with release changes; `.config/` and `release/` are ignored by Git.

`make publish` requires all code changes to be committed first and asks you to confirm the full target version with `y` (`n` cancels). It increments the build number on the same base version by default, commits only `VERSION`, creates a tag such as `v0.8.1-build4`, and pushes to `origin` so Actions can publish the release. To raise the base version, use `make publish VERSION=0.9.0`; its first build is `0.9.0-build1`.

Use `scripts/deploy.py` to upload a signed package to a target machine and confirm the restarted version. See the [CD setup and commands](docs/UPDATING.md#本地脚本自动部署cd).

Pushing `main` runs tests and builds signed packages in GitHub Actions. Pushing a `vx.y.z-build{c}` tag that exactly matches the committed `VERSION` publishes a GitHub Release. Configure repository signing credentials first; see [automated builds and releases](docs/GITHUB-ACTIONS.md).

- [Developer guide](docs/DEVELOPING.md): architecture, command-line options, tests, Docker, and cross-platform builds.
- [Update release guide](docs/UPDATING.md): signing keys, versioning, package format, and recovery.
- [Windows security and signed releases](docs/WINDOWS-SECURITY.md): system warnings and Windows code signing.

## License

[MIT](LICENSE)
