# Lan Copy 开发者文档 / Developer Guide

> 中英双语开发文档（中文在前，English follows）/ Bilingual developer documentation (Chinese first, English follows)

## 目录 / Contents

- [项目结构 / Project Layout](#项目结构--project-layout)
- [环境要求 / Requirements](#环境要求--requirements)
- [本地运行与测试 / Run & Test](#本地运行与测试--run--test)
- [构建制品 / Building Artifacts](#构建制品--building-artifacts)
- [交叉编译 / Cross Compiling](#交叉编译--cross-compiling)
- [Docker](#docker)
- [命令行参数 / Command-line Flags](#命令行参数--command-line-flags)
- [Windows 控制台行为 / Windows Console Behavior](#windows-控制台行为--windows-console-behavior)
- [架构说明 / Architecture](#架构说明--architecture)
- [品牌标识 / Brand Assets](#品牌标识--brand-assets)
- [发布流程 / Release Process](#发布流程--release-process)

## 项目结构 / Project Layout

```text
lan-copy/
├── main.go, startup.go    # 薄入口与构建变量 / thin entry point and build variables
├── assets.go             # go:embed 前端资源 / embedded frontend assets
├── internal/
│   ├── app/              # 组件组装、启动恢复与关停 / assembly, startup recovery, shutdown
│   ├── httpserver/       # 路由、HTTP 处理、SSE / routes, handlers, SSE
│   ├── filestore/        # 文件操作与上传事务 / files and upload transactions
│   ├── textstore/        # 文本持久化 / shared-text persistence
│   ├── tablestore/       # 智能表格 CSV 解析、标记、查询与原子更新 / smart-table CSV marks, queries and atomic updates
│   ├── printing/         # Windows 打印版面、测试页与文件任务 / layout, test pages and document jobs
│   ├── updater/          # 更新验签、安装与回滚 / update verification, install, rollback
│   ├── plugin/           # 插件注册、生命周期、事件推送、协议与 HTTP Client / registry, lifecycle, events, protocol and client
│   │   └── dockerruntime/ # Docker/Podman 运行时适配 / Docker and Podman runtime adapter
│   └── desktop/          # 自启动与平台桌面能力 / autostart and desktop integration
├── web/                  # 前端资源，go:embed 内嵌 / Frontend assets embedded via go:embed
├── frontend/             # Vue/TypeScript 源工程 / Vue and TypeScript sources
├── tmp/frontend-dist/    # Vite 临时构建输出，不供 Go 嵌入 / temporary Vite output, not embedded
├── lan-copy.syso         # 编译时自动链接的图标资源 / Icon resource linked by go build
├── build.bat             # Windows 快捷构建脚本 / Quick build script (Windows)
├── Makefile              # 全平台制品构建 / Cross-platform artifact builds
└── docs/DEVELOPING.md    # 本文档 / This document
```

## 环境要求 / Requirements

Go 1.24 或更新版本。运行程序除 Docker 插件管理所需的官方 Engine SDK 外只依赖标准库；发布打包还需要 Python 3，并会用固定 Node/npm 校验和构建前端产物。
Go 1.24+. The runtime only needs the Go standard library plus the official Docker Engine SDK used by the plugin manager; release packaging also needs Python 3 and uses the pinned Node/npm toolchain to check and build frontend assets.

前端迁移工程固定使用 Node.js 24.18.0 与 npm 11.16.0；安装依赖需联网，运行已构建的 Go 程序不需要 Node.js。
The frontend migration source pins Node.js 24.18.0 and npm 11.16.0. Dependency installation needs network access; running a built Go app does not need Node.js.

## 本地运行与测试 / Run & Test

```bash
go run .            # 运行 / run
make test           # Python 与 Go 测试 / Python and Go tests
make release-test   # 发布前在 macOS 本机跑 CI 同款检查（含 darwin 测试）/ local macOS checks before publishing
go vet ./...        # 静态检查 / static check
make frontend-check # npm ci、类型检查、前端测试、Vite 构建与产物校验 / npm ci, typecheck, frontend tests, Vite build and bundle check
make frontend-sync # 重新生成前端 bundle 并同步到 web/ / rebuild and sync the frontend bundle into web/
make frontend-release # 发布构建使用：校验、构建并同步前端 / release frontend pipeline
make frontend-dev   # 启动 Vite 页面并代理到本机 Go 服务 / run Vite pages with a proxy to Go
```

`frontend/` 是人工维护的 Vue/TypeScript 源码，`web/` 保存由 Vite 生成并提交的生产资源。内网空间、设置、碎片快传、打印队列、插件服务、分享/状态与独立预览入口均由 Vue 管理。日常单独构建 Go 的 `go build .` 使用已提交的静态资源，不要求 Node.js；发布构建 `make build` 会自动安装前端依赖、运行类型检查和测试、构建前端并同步嵌入资源，再打包 Go 制品，因此需要 Node.js/npm。剩余人工验收见 [v0.5.0 实施进度](v0.5.0-progress.md)。

内网空间 `/` 与碎片快传 `/transfer-text` 共用 `src/pages/app.ts` 和 `App.vue`，以 History API 在同一 SPA 中切换；`/transfer.html` 是碎片快传的静态页面入口，直接返回同一页面骨架，不再做 HTTP 跳转；两个 HTML 构建入口保留初始加载骨架。公共顶栏、页脚和预览弹窗位于 `src/components/`，分享/状态浮层共用 `src/composables/usePopover.ts`。导航保留文本草稿及文件夹地址；页面卸载清理 SSE、请求、定时器和右键菜单注册。目录树由 `FileTree.vue` 共享给内网空间侧栏和碎片转存弹窗。碎片快传的 `FragmentTransfer.vue` 管理文字和图片的混合发布、纯文本编辑、到期显示、瀑布流卡片、长文展开、粘贴/拖拽上传及删除；已发布文字仍按轻量 Markdown 子集渲染；`FragmentSaveDialog.vue` 通过 `/api/filesystem` 选择目录并调用 `/api/texts/{id}/save` 转存。`preview.html` 继续作为独立预览入口。
本地开发时先以 `go run . -listen :8080` 启动服务，再运行 `make frontend-dev`；Vite 默认代理到 `http://127.0.0.1:8080`，可用 `LAN_COPY_PROXY` 指定其他 Go 地址。开发代理会把同源动作请求的 Origin 规范到 Go 目标，不影响生产访问策略。
Run `go run . -listen :8080` before `make frontend-dev`. Vite proxies to `http://127.0.0.1:8080` by default; set `LAN_COPY_PROXY` to use another Go instance. The dev proxy normalizes action request Origins to the Go target without changing production access policy.
`frontend/` contains manually maintained Vue/TypeScript sources; `web/` contains the Vite-generated production assets committed with the source. Shared files, settings, text transfer, sharing/status controls, and standalone preview are managed by Vue pages. A standalone `go build .` uses the committed assets and does not need Node.js. The release command `make build` automatically installs frontend dependencies, runs type checks and tests, builds the frontend, syncs its embedded assets, and then packages the Go binaries; it requires Node.js/npm. Remaining manual checks are tracked in [the v0.5.0 progress record](v0.5.0-progress.md).

Windows 下也可使用 `build.bat` / On Windows you can also use `build.bat`：

| 命令 / Command | 作用 / Purpose |
|------|------|
| `build.bat build` | 构建到 `release\lan-copy.exe` / build to `release\lan-copy.exe` |
| `build.bat release` | 签名并校验 Windows 发布包 / build and verify signed Windows release packages |
| `build.bat test` | 运行测试 / run tests |
| `build.bat run` | 直接运行 / run directly |
| `build.bat clean` | 清理构建产物 / clean artifacts |

## v0.5.6 文件打印与预览

`/print-queue` 新增本地 PDF、JPG/JPEG、PNG 的选择和打印预览，支持 PDF 翻页、预览放大/缩小；图片等比居中，透明区域铺白底。纸张及方向来自 Windows 主机当前打印机设置，固定一份、单面，打印全部页面。内网空间单个支持文件的「⋯ → 打印」通过 `/print-queue?file=...&from=...` 自动载入预览，普通点击文件名保持原行为。共享来源通过既有 `/preview-files/{name}` 和 filestore 边界读取成有 50 MiB 上限的浏览器快照，避免预览后重新读取变化内容；失效读取可取消。刷新可重新加载共享来源，返回保留原文件夹；改选本地文件会清除共享来源参数。原文件不会加入内网空间。单个原文件最多 50 MiB、PDF 最多 50 页。

浏览器复用内嵌 PDF.js 与图片解码生成 200 DPI 的 PNG 页面；预览和提交打印使用相同的布局与渲染代码，CSS 边界线和缩放不会进入输出。新增 `GET /api/printing/layout?printer=...` 返回物理纸张、可打印区域及版面标识；`POST /api/printing/documents` 接收 manifest 和顺序命名的 PNG 页面，必须带 `X-Lan-Copy-Action: print-document` 并通过同源校验。后端接收的是已渲染页面，不独立解析原始 PDF。共享 SPA 的 CSP 允许本地 PDF.js 所需的 WebAssembly 编译，仍不允许 JavaScript eval。

`internal/printing` 管理互斥、暂存、完整 PNG 校验和清理；所有页面验证完成才进入 GDI。限制单页 12 MiB/1600 万像素、合计 128 MiB；暂存于私有应用数据目录 `print-spool/`，成功/失败/接收中断后清理，启动丢弃已确认布局的自有遗留页，保留含未知内容的目录，绝不重放任务。Windows 使用驱动返回的完整 DEVMODE，设置支持的份数/单双面字段，固定系统线程调用 GDI。提交前再次核对版面标识，设置变化时要求刷新预览。

任务新增文档名称、页数、接收状态和独立的清理警告；已提交不能推断实际出纸。浏览器回归使用实际 PDF.js/图片和 HTTP multipart 上传，但打印机信息及原生提交由测试接收器模拟，不会实际出纸。Windows ABI 测试与实际设备验收分别记录，见 [v0.5.6 实施进度](v0.5.6-progress.md)。

## v0.5.5 打印队列首期

顶栏分享按钮左侧的打印入口进入 `/print-queue`，与内网空间、碎片快传共用 SPA。`frontend/print.html` 提供直接访问的加载骨架，由 Vite 构建并同步到 `web/print.html`。

Windows 主机通过系统已安装的打印机和驱动提交一张 Lan Copy 测试页，不需要开启 Windows 打印机共享或安装额外打印工具。`internal/printing` 负责原生枚举、GDI 测试页及本次启动后的最近 50 条任务；HTTP 只负责动作来源校验和协议。其他平台展示不支持提示。任务“已提交”不代表已出纸，实际结果在设备或 Windows 队列中确认。本版不包含普通文件打印或完整系统队列管理。

浏览器回归对 Windows 响应进行模拟，保证不会在开发者打印机上实际出纸；交叉构建不能替代 Windows 实机打印验收。实际记录见 [v0.5.5 实施进度](v0.5.5-progress.md)。

## v0.5.1 前端产物与验证 / Frontend artifacts and validation

- `frontend/index.html`、`transfer.html`、`print.html`、`plugins.html`、`preview.html` 是页面唯一来源，公共样式源为 `frontend/src/style.css`。Vite 生成的 HTML 同步到 `web/`，完整资源树同步到 `web/_frontend/`，禁止手改这些产物。`web/logo/` 与 `web/vendor/` 保持独立维护，不由同步脚本覆盖。
- `frontend/scripts/sync-assets.mjs` 校验静态/动态依赖、CSS、字体/图片和 HTML 引用，检查新增、删除及内容变化。替换失败回滚已有资源；临时副本位于不参与内嵌的 `tmp/`。
- HTML 与固定路径资源使用 `Cache-Control: no-cache`；存在的 hash 资源使用长期 immutable 缓存。旧标签页升级后请求已删除的懒加载 chunk 时自动刷新一次，连续失败显示手动重试，避免刷新循环。
- Go 直接构建仍使用仓库中的已提交产物；开发完成需运行 `make frontend-sync` 并一起提交 HTML 和资源。`make frontend-check` 与发布构建执行完整产物及离线 vendor 校验。

页面使用固定 Node.js/npm；先在 `frontend/` 执行 `nvm use`，确认 `node --version` 为 `v24.18.0`、`npm --version` 为 `11.16.0`，不符合时安装指定工具链，不绕过 `engine-strict`。

```bash
cd frontend
nvm use
npm ci
npm run check
npm run check-assets
npx playwright install chromium firefox webkit
npm run test:browser
```

浏览器测试使用 Playwright `1.58.2`（开发依赖），自动构建并启动隔离的真实 Go 服务，创建临时 PDF/DOCX/XLSX/文本样本；覆盖两种预览入口、关闭与断网、Range、文本发布、缓存头及 A/B 二进制升级后的旧 chunk 恢复。测试退出清理临时数据和服务，不操作用户共享目录。Linux CI 使用 `npx playwright install --with-deps chromium firefox webkit` 安装浏览器及系统依赖。WebKit 自动化不等于真实 Safari 或 iOS 验收。

`frontend/package.json` 与 lockfile 固定预览依赖版本和来源。Node 脚本从已安装依赖生成 `/vendor/` 资源；检查命令比较生成树与提交资源，不下载上游文件：

```bash
npm ci
npm run check-vendor
npm run sync-assets
npm run check-assets
```

更新 vendor 时调整精确依赖版本或来源并更新 `package-lock.json`，然后生成、检查并提交 `web/vendor/`。开发和 Go 运行时不会下载资源；全新 npm 安装需要可访问锁文件中的包来源。

`frontend/` contains the HTML, CSS and vendor source files. `web/*.html`, `web/_frontend/` and `web/vendor/` are generated from the frontend sources and npm dependencies, then checked together. Runtime and plain Go builds use committed embedded files and need neither Node.js nor network access. Run the pinned toolchain checks and `npm run test:browser` for embedded-server integration. See [v0.5.7 progress](v0.5.7-progress.md) for current validation.

## 构建制品 / Building Artifacts

在 macOS 或 Linux 上用 Make 默认交叉构建 Windows amd64 和 macOS Apple Silicon 制品并生成校验和。
On macOS/Linux, Make builds Windows amd64 and macOS Apple Silicon packages plus checksums by default.

```bash
make build          # 全部制品 + SHA256SUMS，构建号 +1 / all artifacts + SHA256SUMS, build +1
make windows        # 仅 Windows / Windows only
make macos          # 仅 macOS / macOS only
make clean          # 清理制品 / clean artifacts
make dist-clean     # 删除 release 目录 / remove release dir
```

`VERSION` 使用 `x.y.z-build{c}`，例如 `0.8.1-build3`。`make build` 成功后把构建号加一并写回 `VERSION`；基础版本升级时重置为 `-build1`。`make windows`、`make macos` 和 `make artifacts` 只使用已保存版本，不递增。 / `VERSION` uses `x.y.z-build{c}`, for example `0.8.1-build3`. A successful `make build` increments and saves the build number; raising the base version resets it to `-build1`. `make windows`, `make macos`, and `make artifacts` use the saved version without incrementing.

输出保存在 `release/` / Output lands in `release/`：

```text
Lan-Copy-Windows-amd64.zip
Lan-Copy-macOS-Apple-Silicon.tar.gz
SHA256SUMS
```

Windows 包含 `lan-copy.exe`，双击后会自动打开浏览器，并附带离线状态页 `使用说明.html`。macOS 包含 README 中介绍的命令行启动方式。扩展 Windows 架构时可运行 `make windows WINDOWS_ARCHES="amd64 arm64"`。
The Windows archive contains `lan-copy.exe` that opens the browser when double-clicked, plus the offline `使用说明.html` status page. The macOS archive uses the command-line launch instructions in its README. To add Windows ARM64, run `make windows WINDOWS_ARCHES="amd64 arm64"`.

压缩包内含可执行文件和 README；Windows 包另附离线状态页。Windows 版为带图标的控制台程序（未用 `-H=windowsgui`）。
Archives contain the binary and README; the Windows package also includes an offline status page. The Windows binary is a console app with an embedded icon (no `-H=windowsgui`).

## 交叉编译 / Cross Compiling

```bash
# Windows x64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o lan-copy.exe .
# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o lan-copy-macos-arm64 .
# macOS / Linux 本机 / native
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o lan-copy .
```

## Docker

```bash
docker build -t lan-copy .
docker run --rm -p 8080:8080 -v "$PWD/lan-copy-data:/data" lan-copy
```

插件管理需要访问 Docker 或 Podman Engine（启动时自动检测；优先 Docker，可用 `DOCKER_HOST` / `CONTAINER_HOST` 覆盖）。Podman 使用 Docker 兼容 API，Socket 通常为 `/run/podman/podman.sock`（rootful）、`$XDG_RUNTIME_DIR/podman/podman.sock`（rootless）或 `podman machine` 的 socket。

Podman 安装后 Docker 兼容 API 的 socket 可能未启用。Lan Copy 自动探测失败且检测到 Podman 时，会按当前身份执行一次 `systemctl --user start podman.socket`（root 为 `systemctl start podman.socket`），只启动不设置开机自启；`LAN_COPY_DISABLE_PODMAN_AUTOSTART=1` 可关闭。自动启动失败时再手动启用：

```bash
systemctl --user enable --now podman.socket   # rootless
sudo systemctl enable --now podman.socket     # rootful
```

如果自动检测失败，页面会列出已尝试的端点；也可以显式指定：

```bash
DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock ./lan-copy
```

运行时不可用时，插件页还提供“启动 Podman”按钮：Linux 执行 `systemctl` 启动 socket，Windows/macOS 执行 `podman machine start`；启动完成后自动重新探测，无需重启。该接口对局域网设备开放，仅依赖同源与动作头，请只在可信网络使用。

宿主机直接运行二进制时默认使用 `loopback` 模式；将 Lan Copy 本身放入容器时，必须加入插件专用网络并把对应运行时 Socket 交给主服务（不要交给插件）：

```bash
docker network create plugin-network
docker run -d --name lan-copy --network plugin-network -p 8080:8080 \
  -v "$PWD/lan-copy-data:/data" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  lan-copy -plugin-network-mode=container
```

协议、镜像构建、安全边界与 API 见 [插件服务协议](PLUGIN-PROTOCOL.md)。

Podman 在 Windows/macOS 上通过 `podman machine` 运行。此时 `host.containers.internal` 只指向虚拟机，Lan Copy 会按固定规则选择宿主局域网 IPv4 注入 `LAN_COPY_API_URL`，并在插件页只读显示地址与理由；`-plugin-api-url` 仍可覆盖。重启插件会重建容器并重新注入，普通启动沿用已有容器。详见协议第 8.1 节。

## 命令行参数 / Command-line Flags

```text
-listen string   监听地址 / listen address（默认 / default ":8080"）
-dir string      文件存储目录 / storage directory（中文系统 / Chinese OS "共享文件夹-lancopy"，其他 / other "shared-folder-lancopy"）
-max-gb int      单次上传总大小上限 GiB / max upload size per batch in GiB（默认 / default 10）
-plugin-network-mode string  插件网络模式 loopback|container / plugin network mode（默认 / default "loopback"）
-plugin-api-url string  注入插件的 Lan Copy API 地址 / API base URL injected into plugins（默认自动推断；Podman machine 使用宿主局域网 IPv4 / inferred by network mode and runtime）
-plugin-network string      插件专用 Docker 网络名称 / plugin Docker network（默认 / default "plugin-network"）
```

示例 / Example：

```bash
./lan-copy -listen :9000 -dir "$HOME/Downloads/LanCopy" -max-gb 20
```

上传资源限制可在设置 → 资源中由运行 Lan Copy 的本机修改，也可编辑共享目录同级应用数据目录中的 `upload-resources.json` 并重启服务。默认配置为 3 个并发上传、20 GiB 全局暂存空间和 60 秒读取空闲超时；范围分别为 1–32、1–1024 GiB、5–600 秒。配置文件只在网页保存后创建。同一页面的“资源详情”显示共享文件、按类型聚合的空间占用、上传任务、暂存空间、下载任务、Lan Copy 进程和运行中插件的 CPU/内存；运行数据每 2 秒刷新，文件类型统计只在打开或手动刷新时扫描。访客可查看统计，但不能修改限制。Lan Copy 自身内存使用 Go 运行时 `MemStats.Sys`，CPU 按单核百分比显示；插件统计来自容器运行时。单次上传总大小仍由 `-max-gb` 控制。目录上传先通过清单预检返回冲突，再传输文件；预检会话 15 分钟过期，最终提交仍会检查冲突。

## macOS 后台启动 / macOS Background Launch

中文：Homebrew 安装的命令名为 `lancopy`。在 macOS 终端直接执行 `lancopy`（无参数）时，程序先探测 `-listen` 地址是否已有实例，随后以 `--internal-background` 重新执行自身：子进程用 `Setsid` 脱离终端，stdin 指向 `/dev/null`，stdout/stderr 追加到 `~/Library/Logs/lancopy/lancopy.log`（`LAN_COPY_LOG_FILE` 可覆盖，超过 8 MiB 时轮转为 `lancopy.log.1`），只把启动摘要通过 fd 3 回传给父进程打印。插件探测等日志只写入日志文件，不出现在终端。父进程最多等待 15 秒；启动失败时打印日志尾部并以非零码退出，超时会终止本次启动的子进程。

只有命令名恰为 `lancopy`、无参数、stdout 为真实 TTY 时才自动后台。发布包内的 `lan-copy`、`go run .`、`make build` 产物、带任意参数的启动以及 `brew services` 都保持前台；Windows 与 Linux 不受影响。停止后台实例使用页面右上角“关闭软件”（`POST /api/shutdown`）。

English: The Homebrew command is `lancopy`. A bare `lancopy` on a macOS terminal probes the `-listen` address for an existing instance, then re-executes itself with `--internal-background`: the child calls `Setsid`, points stdin at `/dev/null`, appends stdout/stderr to `~/Library/Logs/lancopy/lancopy.log` (override with `LAN_COPY_LOG_FILE`, rotate to `lancopy.log.1` above 8 MiB), and returns only the startup summary to the waiting parent through fd 3. Plugin detection and other details stay in the log file and never reach the terminal. The parent waits up to 15 seconds, prints the log tail and exits non-zero on failure, and kills the child it started on timeout.

Auto-backgrounding applies only when the binary is named `lancopy`, has no arguments, and stdout is a real TTY. The release binary `lan-copy`, `go run .`, `make build` output, any flag-bearing launch, and `brew services` stay in the foreground; Windows and Linux are unchanged. Stop a background instance with **关闭软件** (Shut Down) at the top right of the page (`POST /api/shutdown`).

## Windows 控制台行为 / Windows Console Behavior

中文：从资源管理器双击启动时显示启动信息与访问地址，自动打开默认浏览器，并提示“按任意键关闭本窗口”；按键后调用 `FreeConsole()` 关闭控制台，服务继续在后台运行。停止服务请从本机 `localhost` 页面操作（`/api/shutdown`）；若有上传或下载在途，需查看风险提示并输入“强制关闭”确认。重复启动时，无论双击还是从终端启动，都会探测已有实例并打开其浏览器页面，随后退出第二个进程，不启动第二份服务。`/healthz` 必须返回 `X-Lan-Copy-Service: lan-copy` 和 `ok`；并发首次启动时会短暂重试，端口被其他程序占用则报告错误。旧版本没有身份响应头，升级时需先关闭旧服务。

English: when launched by double-clicking in Explorer, the console prints startup info, opens the default browser, and waits for any key; the key press calls `FreeConsole()` so the console closes while the service keeps running. Shut it down from the local `localhost` page (`/api/shutdown`); if uploads or downloads are active, review the warning and type “强制关闭” to confirm. Repeated launches from Explorer or a terminal probe the occupied address, verify the Lan Copy health marker, open the existing page, and exit without starting another server. Probes briefly retry during concurrent startup. Stop older versions once when upgrading: they lack the service identity header.

实现位于 `internal/desktop/console_windows.go`（`console_other.go` 为空实现）。仅当进程独占控制台（`GetConsoleProcessList` 只有一个进程，即双击场景）时启用控制台关闭交互；重复启动识别不受此限制。浏览器、文件和目录均通过 Windows 原生 `ShellExecuteW` 打开。

The logic lives in `internal/desktop/console_windows.go` (`console_other.go` is a stub). Console detachment only activates when the process exclusively owns its console. Repeat-launch detection is independent of console ownership. Windows opens browsers, files and folders through ShellExecuteW.

## 架构说明 / Architecture

- 单二进制：根目录将 `web/` 通过 `go:embed` 内嵌并传给 `internal/app`；`app` 组装组件和 `internal/httpserver`，后者在 `net/http.ServeMux` 注册路由。`filestore`、`textstore`、`updater`、`desktop`、`plugin` 不依赖 HTTP 层。`plugin` 通过 `Runtime` 接口隔离运行时，只有 `plugin/dockerruntime` 导入 Docker SDK；该包在启动时探测 Docker 与 Podman 端点，Podman 复用同一 Docker 兼容 API。
- API 一览：
  - `GET /api/files` 列表（文件与目录列表以 previewType 表示预览能力，不支持的格式为空字符串）。目录上传先用 `POST /api/upload-tree/preflight?path=...` 提交 JSON 清单（`entries: [{path, directory}]`），响应包含一次性会话和冲突列表；确认后使用 `POST /api/upload-tree?path=...&session=...` 以 multipart 提交，首项为 `manifest` JSON（`entries`、`replace`），文件体字段为对应下标的 `file-N`。会话 15 分钟过期，取消时可调用 `DELETE /api/upload-tree/session/{id}`。旧版 `POST /api/upload` 已移除。相对路径逐段使用共用规则校验，拒绝越界、内部保留名前缀（不区分大小写）、Windows 保留设备名、非法字符、文件/目录冲突和清单内大小写别名；最多 10000 项（含隐含父目录）、64 个路径组件、2 MiB 清单，请求总字节数仍受 `-max-gb` 限制。内容流式写入隐藏的 `.upload-txn-*` 事务目录；并发上传、全部暂存文件字节和请求体空闲时间分别受设置页配置约束。完成后在锁内以同文件系统改名提交，并重新校验目标。普通文件同名时自动生成编号名称；目录冲突需确认后整体替换。事务记录、暂存内容与替换备份同处一个目录，进程重启时回滚未完成提交。拖拽使用目录 API 循环读取所有分页并保留空目录，目录选择按钮受浏览器能力限制，可能不包含空目录
  - `GET/PUT /api/settings/upload-resources` 读取或修改上传全局资源限制；访客只读，PUT 仅接受本机同源请求并要求 `X-Lan-Copy-Action: upload-resources`。配置含 `concurrentUploads`、`stagingGiB`、`idleTimeoutSeconds`。
  - `GET /api/resource-usage` 返回只读占用统计：`storage`（已占字节、文件数和按 `kind` 聚合的 `types`）、`uploads`（进行中与并发上限）、`staging`（已预留字节与配置上限）、`downloads`（进行中数量）。响应 `Cache-Control: no-store`，不含文件名和传输明细，局域网访客可读；共享存储统计失败时返回 500。
  - `GET /api/system-usage` 返回 Lan Copy 进程与运行中插件容器的实时 CPU/内存：`app` 含 `cpuPercent`、`memoryBytes`、`cpuReady`、`sampledAt`；`plugins` 含合计、运行/失败数量和每个插件的 `items`。进程 CPU 由累计 CPU 时间差分计算，首次采样 `cpuReady=false`；插件聚合复用 2 秒 stats 缓存，HTTP 层限时 3 秒，单项失败只标记该项不可用。响应 `Cache-Control: no-store`，访客可读。
  - `GET /thumbnails/{name}` 图片缩略图：JPEG/PNG/GIF 按比例缩至最长边 128px，限制源文件大小、像素数和并发解码；其他图片格式使用浏览器图片展示。缩略图 URL 携带修改时间和大小以更新浏览器缓存
  - `GET /preview-files/{name}` 预览资源（类型白名单、固定 MIME、inline、Range/HEAD；SVG 以 image/svg+xml 提供并用 sandbox CSP 隔离）；`/preview.html?path=...` 为预览页，文本最多读取前 256 KiB。PDF 使用内嵌 PDF.js 6.4.299 单页渲染，支持翻页和缩放。DOCX 由浏览器端 `docx-preview` 转成 HTML 预览，限制源文件 32 MiB，禁用嵌入 HTML 部件、评论和修订；不支持旧版 `.doc` 预览，仍可下载原文件。XLS/XLSX 由 Web Worker 内的 SheetJS 0.20.3 解析，单文件限制 32 MiB，每张工作表最多显示 250 行、50 列，最多显示 20 个工作表；公式显示缓存值，宏不执行。DOCX、SheetJS 预览资源版本、来源、完整性和许可证分别见 `web/vendor/docx-preview/README.md`、`web/vendor/sheetjs/README.md`；PDF.js 资源见 `web/vendor/pdfjs/README.md`。
  - `GET /files/{name}` 下载、`DELETE /api/files/{name}` 删除（嵌套路径作为整体 URL 编码；列表递归包含子文件夹中的文件）
  - `GET /api/filesystem?path=...&q=...` 读取目录（空路径为共享根目录）；指定 q 时递归搜索当前目录和子目录中的文件、文件夹名称，单次最多遍历 10000 项；客户端取消请求会中止后续遍历
  - `POST /api/filesystem/{mkdir|move|rename|delete}` 目录操作，JSON 字段为 `path`、`destination`、`name`；不覆盖同名目标，禁止越界路径和符号链接，非空目录删除先返回 409 和 requiresConfirmation=true，经二次确认后以 recursive=true 重试递归删除，根目录不可删除

  - `/` 内网空间首页，`/transfer-text` 碎片快传页面（文字、图片碎片的发布、复制、转存和删除；文件上传与浏览统一在内网空间），`/transfer.html` 直接返回同一碎片快传页面；旧地址 `/filesystem.html` 已移除；文件夹对应存储目录中的真实目录，无额外索引数据库
  - `/plugins` 插件服务页面：入口位于“设置 → 插件”，在新标签页打开。管理 Docker/Podman 插件镜像、安装与生命周期、配置、健康记录和日志，并在“对接”菜单查看事件目录、投递状态、发送测试事件和智能表格 API 接入方式。页面子路由为 `/plugins/installed`、`/plugins/images`、`/plugins/integration`，`/plugins` 默认进入插件列表。API 位于 `/api/plugins*`，局域网可访问；写操作要求 `X-Lan-Copy-Action: plugins`（镜像上传为 `plugins-image`）且浏览器调用必须同源。详见 [插件服务协议](PLUGIN-PROTOCOL.md)
  - `GET /api/plugins/event-catalog`、`GET /api/plugins/{id}/events`、`POST /api/plugins/{id}/events/test`：插件生命周期事件目录、每插件投递状态与最近记录、测试事件。上传、文件夹/文件操作和文本操作成功后异步推送；事件队列、重试和签名均不阻塞 HTTP 响应。
  - `/plugin-app/{id}/...` 插件 Web 界面反向代理：Manifest 声明 `ui` 后，把局域网请求转发到运行中且健康的实例，转发 `X-Forwarded-Prefix`，改写根绝对路径的 `Location`，支持 SSE、上传下载和 WebSocket。停止或不健康返回 503，未声明界面返回 404。
  - 智能表格内联视图：共享目录中的 CSV 默认是普通文件；在内网空间文件行菜单标记为智能表格后，文件行显示“表格”标记，点击或双击打开单文件内联视图，支持全文搜索（输入即搜并高亮匹配）、排序、分页、列顺序调整、列隐藏、行新增/编辑/删除。标记保存在应用数据目录的 `table-marks.json`，由 `POST /api/tables/mark` 切换，`GET /api/filesystem` 返回可选 `table` 字段。`POST /api/tables/query`、`POST /api/tables/create`、`POST /api/tables/lock-and-update` 保留；写操作要求 `X-Lan-Copy-Action: tables`。列顺序与隐藏偏好保存在 CSV 同目录的 `<完整表格文件名>.hidden.metadata.json`，浏览器 `POST /api/tables/query` 响应附带可选 `layout`，`POST /api/tables/layout` 保存（同样要求 `X-Lan-Copy-Action: tables`）。该侧车文件不改变 CSV 和版本，插件 API 响应不含 `layout`；所有文件列表、搜索、下载、预览、缩略图和容量统计都会过滤 `<文件名>.hidden.metadata.json`。插件通过 Bearer 令牌访问 `/api/plugin-api/tables*`，可按路径访问任意合法 CSV，查询和原子更新与内联视图共用同一版本校验逻辑。
  - `GET/POST /api/texts`、`POST /api/texts/fragments`、`DELETE /api/texts/{id}` 共享碎片保存在共享目录同级的 `.lan-copy-app-data-<共享目录名>/` 中；图片通过 `GET /api/texts/{id}/images/{index}` 读取。旧版本在共享目录内留下的文本库和上传事务会在启动时迁入该目录。
  - `GET /api/events` 统一 SSE 变更流，事件名为 `change`，JSON 数据含递增 `version` 及 `files` / `texts` 标记；25 秒心跳。浏览器按需刷新对应列表，断线时页面可见期间每分钟轮询恢复。`GET /api/access` 局域网地址探测
  - `GET /api/shutdown/status` 查询本机在途上传/下载数；`POST /api/shutdown` 网页关停服务（仅 localhost；有在途传输时需提交“强制关闭”确认）；系统信号与更新使用同一关停流程。`GET /healthz` 健康检查

共享目录内的应用路径检查适用于应用自身的请求与事务；本机其他进程直接并发修改共享目录不属于受支持的安全边界。应用当前仍使用路径级文件操作，不能防止外部进程在校验后替换目录项或符号链接。
- 并发模型：文件操作与共享文本分别使用互斥锁；文件/目录列表在文件锁内收集元数据，锁外排序并写响应。文件与文本变更通过同一 SSE 订阅流广播并合并待处理标记；目录切换、搜索和文本刷新会取消已过期的 GET，页面隐藏或关停时取消在途读取。上传先流式写入事务暂存目录，再在文件锁下通过同文件系统改名提交；启动时回滚进程中断的事务。

Single binary: `web/` is embedded via `go:embed`; routes are registered on `net/http.ServeMux`. File and text changes share a versioned SSE stream; the browser cancels stale list reads and falls back to visible-page polling when SSE disconnects. Uploads and their recovery records live in `.lan-copy-app-data-<shared-directory-name>/`, beside the shared directory. Transaction commit uses same-filesystem renames under a mutex, so the shared directory must not be a separate mounted filesystem from its parent. Startup moves legacy transaction directories out of the shared folder and rolls back transactions left by process interruption.

## 品牌标识 / Brand Assets

Logo 与 favicon 位于 `web/logo/`，随网页资源一起内嵌进可执行文件。
Logos and favicons live in `web/logo/`, embedded with the rest of the web assets.

| 文件 / File | 尺寸 / Size | 用途 / Usage |
|------|------|------|
| `lan-copy-logo.svg` | 120x120 | 主 logo / primary logo |
| `favicon-16x16.svg` | 16x16 | favicon |
| `favicon-32x32.svg` | 32x32 | favicon |
| `logo-192x192.svg` | 192x192 | 高分辨率图标 / high-DPI icon |

颜色方案 / Palette：主蓝 primary `#3977f6` · 深蓝 dark blue `#2362df` · 深色背景 dark background `#14213d`

exe 图标来自根目录预生成的 `lan-copy.syso`，`go build` 会自动把它链接进可执行文件。仓库内不再保留图标生成工具；如需更换图标，用任意工具生成新的 `.syso` 覆盖该文件即可。

The exe icon comes from the pre-generated `lan-copy.syso` in the repo root, which `go build` links automatically. The icon-generation tooling is not kept in the repo; to change the icon, generate a new `.syso` with any tool and overwrite the file.

## 发布流程 / Release Process

GitHub Actions 已支持推送 `main` 自动构建、推送版本标签后发布 Release；一次性签名配置及版本规则见 [GitHub 自动构建与发布](GITHUB-ACTIONS.md)。CI 使用已提交的完整版本（含构建号），不执行自动递增。CI 没有任何 macOS runner，只跑 Linux 与 Windows；macOS 专属验证与 Homebrew 更新由本机 `make` 目标完成。

GitHub Actions builds on pushes to `main` and publishes releases on matching version tags. See [automated builds and releases](GITHUB-ACTIONS.md) for signing configuration and version rules. CI uses the committed full version, including the build number, without incrementing it. CI has no macOS runner and runs on Linux and Windows only; macOS-specific verification and the Homebrew tap update run locally through `make` targets.

### 本地 macOS 验证与 Homebrew 发布 / Local macOS verification and Homebrew

macOS 专属步骤全部在本机执行，CI 不再消耗 macOS runner（托管 arm64 机器容量不足时会长时间排队并被取消）：

| 目标 | 用途 |
| --- | --- |
| `make release-test` | 确认 macOS 后运行 `go vet ./...`、脚本测试与 `go test ./...`，覆盖 darwin 专属测试；`make publish` 已自动调用 |
| `make homebrew-check` | 只读校验本地 Tap 公式：`ruby -c`、`brew style`、`brew audit --strict --online --formula jachy-h/jachy-h/lancopy` |
| `make homebrew` | 按已发布 Release 的 `SHA256SUMS` 改写 Tap 公式、校验、提交并推送；支持 `DRY_RUN=1`、`NO_PUSH=1`、`CHECKSUMS=<file>`、`HOMEBREW_WAIT=<秒>`，网络受限时用 `HOMEBREW_AUDIT_ONLINE=0` 跳过 `brew audit` 的在线 URL 检查 |
| `make browser-test` | 可选：本机跑 Playwright 浏览器集成测试（macOS 上的 webkit 是原生构建）。常规门禁仍在 CI 的 ubuntu job，发布链路不依赖它 |

前置条件：macOS、可用的 Homebrew、已执行 `brew tap jachy-h/jachy-h`、Go 与 Node 按上面的环境要求就绪。Tap 推送使用本机 git 凭据：`brew tap` 默认克隆 HTTPS remote，需要 `gh auth setup-git` 或把该 remote 改为 SSH，否则推送会失败（脚本会保留本地提交并提示修法）。详见 [GitHub 自动构建与发布](GITHUB-ACTIONS.md) 的“Homebrew Tap 由本机更新”一节。

All macOS-only steps run locally; CI no longer uses a macOS runner. `make release-test` runs the darwin test suite (already wired into `make publish`), `make homebrew-check` validates the local tap formula, `make homebrew` rewrites the formula from the published release checksums, verifies it, and pushes it, and `make browser-test` is an optional local Playwright run (the release pipeline does not depend on it).

1. 更新两个 README 与本文档 / update both READMEs and this doc
2. `go vet ./... && go test ./...` 全部通过 / all green
3. `make build` 生成 `release/` 制品与 `SHA256SUMS`
4. Windows 正式发布：在 Windows 上配置受信任的代码签名证书后运行 `build.bat release`，使用 `release/windows-signed/` 中的 Windows 包及其校验和替代未签名包。详见 [Windows 安全与签名发布](WINDOWS-SECURITY.md)。合并不同平台的校验和时，必须使用最终上传文件的哈希。 / For public Windows releases, run `build.bat release` on Windows with a trusted signing certificate and publish packages from `release/windows-signed/`. Use hashes of the final uploaded files when combining platform checksums.
5. 在 GitHub Releases 上传全部制品并发布 / upload all artifacts to GitHub Releases and publish

默认存储目录按服务端系统语言命名；未指定 `-dir` 且新目录不存在时，将原 `lan-copy-data` 目录整体改名并保留其中内容。若新旧目录同时存在，使用新目录并保留旧目录，不自动合并。

应用内部数据目录位于共享目录同级，名称为 `.lan-copy-app-data-<共享目录名>`，保存共享文本库、上传暂存、事务记录和替换备份。首次启动会将旧版留在共享目录中的这些内部文件迁入应用数据目录；若同名旧数据和新数据同时存在，启动会报错并保留两边内容，避免自动覆盖。共享目录需与其父目录处于同一文件系统，以支持事务提交所需的原子改名。

## 应用更新 / Application updates

设置弹窗、更新包格式、签名和进程恢复机制见 [UPDATING.md](UPDATING.md)。新增接口：`GET /api/app`（版本、平台、更新可用性）和 `POST /api/update`（本机原始压缩包上传，要求 `X-Lan-Copy-Action: update`）。运行 `go test ./...` 会实际构建、更新和恢复测试进程；`go test -short ./...` 跳过进程生命周期测试。

## 开机自启动 / Start at Login

设置 → 通用中的开关通过 `GET/PUT /api/settings/autostart` 读写系统启动项。GET 允许访客查看服务主机的实际状态，访客端开关只读（`available=false`）；PUT 仅限本机，要求 `X-Lan-Copy-Action: autostart`，并校验同源请求。开关默认关闭，只影响下一次用户登录；关闭当前服务不会移除启动项。
The General settings switch uses `GET/PUT /api/settings/autostart`. GET lets guests view the service host's actual state with a read-only switch (`available=false`). PUT is local-only and requires the action header and same-origin validation. It is off by default, applies at the next user login, and survives quitting the service.

- macOS：`~/Library/LaunchAgents/com.lan-copy.autostart.plist`，使用 `RunAtLoad`，不配置自动重启，不在切换开关时加载或卸载当前任务。
- Windows：当前用户的 `Software\Microsoft\Windows\CurrentVersion\Run` 中的 `LanCopy` 值；完整参数保存在 `%APPDATA%\LanCopy\startup.json`，避免 Run 命令的长度限制。
- 自启动保存绝对程序路径、工作目录、共享目录、监听地址和上传限制。`--autostart` 跳过浏览器与控制台交互，`--working-dir` 恢复工作目录；这些内部参数会在更新重启后保留。
- 开发运行、其他系统不提供该功能；可用 `LAN_COPY_DISABLE_AUTOSTART=1` 显式禁用。系统设置可能额外阻止启动，网页显示注册状态并提示用户在系统设置中允许。

Registration uses a per-user macOS LaunchAgent or the Windows Run key. Absolute paths and launch options are preserved, and startup does not open a browser. Updates preserve the internal startup flags. Development runs and unsupported systems disable the feature; `LAN_COPY_DISABLE_AUTOSTART=1` disables it explicitly. The UI reports registration state; OS startup/login settings can separately block execution.
