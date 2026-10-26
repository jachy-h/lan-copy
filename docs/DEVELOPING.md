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
│   ├── updater/          # 更新验签、安装与回滚 / update verification, install, rollback
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

Go 1.22 或更新版本。运行程序只依赖 Go 标准库；发布打包还需要 Python 3，并会用固定 Node/npm 校验和构建前端产物。
Go 1.22+. Running the app only needs Go; Release packaging also needs Python 3 and uses the pinned Node/npm toolchain to check and build frontend assets.

前端迁移工程固定使用 Node.js 24.18.0 与 npm 11.16.0；安装依赖需联网，运行已构建的 Go 程序不需要 Node.js。
The frontend migration source pins Node.js 24.18.0 and npm 11.16.0. Dependency installation needs network access; running a built Go app does not need Node.js.

## 本地运行与测试 / Run & Test

```bash
go run .            # 运行 / run
make test           # Python 与 Go 测试 / Python and Go tests
go vet ./...        # 静态检查 / static check
make frontend-check # npm ci、类型检查、前端测试、Vite 构建与产物校验 / npm ci, typecheck, frontend tests, Vite build and bundle check
make frontend-sync # 重新生成前端 bundle 并同步到 web/ / rebuild and sync the frontend bundle into web/
make frontend-release # 发布构建使用：校验、构建并同步前端 / release frontend pipeline
make frontend-dev   # 启动 Vite 页面并代理到本机 Go 服务 / run Vite pages with a proxy to Go
```

`frontend/` 是人工维护的 Vue/TypeScript 源码，`web/` 保存由 Vite 生成并提交的生产资源。共享空间、设置、文本快传、分享/状态与独立预览入口均由 Vue 管理。日常单独构建 Go 的 `go build .` 使用已提交的静态资源，不要求 Node.js；发布构建 `make build` 会自动安装前端依赖、运行类型检查和测试、构建前端并同步嵌入资源，再打包 Go 制品，因此需要 Node.js/npm。剩余人工验收见 [v0.5.0 实施进度](v0.5.0-progress.md)。

共享空间 `/` 与文本快传 `/transfer-text` 共用 `src/pages/app.ts` 和 `App.vue`，以 History API 在同一 SPA 中切换；`/transfer.html` 以 HTTP 308 重定向到 `/transfer-text` 并保留查询参数；两个 HTML 构建入口保留初始加载骨架。公共顶栏、页脚和预览弹窗位于 `src/components/`，分享/状态浮层共用 `src/composables/usePopover.ts`。导航保留文本草稿及文件夹地址；页面卸载清理 SSE、请求、定时器和右键菜单注册。`preview.html` 继续作为独立预览入口。
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

## v0.5.1 前端产物与验证 / Frontend artifacts and validation

- `frontend/index.html`、`transfer.html`、`preview.html` 是页面唯一来源，公共样式源为 `frontend/src/style.css`。Vite 生成的 HTML 同步到 `web/`，完整资源树同步到 `web/_frontend/`，禁止手改这些产物。`web/logo/` 与 `web/vendor/` 保持独立维护，不由同步脚本覆盖。
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

`frontend/vendor-manifest.json` 记录第三方版本、来源、许可、上游 tarball integrity 与每个内嵌文件的 SHA-256。普通校验无需联网：

```bash
python3 scripts/vendor_assets.py --check
python3 scripts/vendor_assets.py --verify-upstream
```

更新 vendor 时，从官方来源确认新版本、许可、下载地址与完整性摘要，编辑清单中相应包的 `version` / `url` / `integrity`（必要时调整选中文件路径），再执行 `python3 scripts/vendor_assets.py --update <name>`；命令先验证上游包摘要，再写入选中文件并重算文件摘要。同步更新对应 README、检查上游安全公告，运行离线与上游校验及受影响预览测试。此流程不让 Go 构建或应用运行联网。清单中的 `projectFiles` 是本项目适配器与说明文件，不能把新增第三方库放入该列表绕过摘要校验。

`frontend/` contains the HTML and CSS sources; `web/*.html` and the complete `web/_frontend/` tree are generated and checked together. Logos and locally vendored libraries remain separately owned. Run the pinned toolchain checks and `npm run test:browser` for embedded-server integration. Vendor checks are offline by default; `--verify-upstream` downloads integrity-pinned archives, and `--update <name>` explicitly refreshes a package after its pin is edited. Runtime and plain Go builds need neither Node.js nor network access. See [v0.5.1 progress](v0.5.1-progress.md) for actual validation and remaining environment limits.

## 构建制品 / Building Artifacts

在 macOS 或 Linux 上用 Make 默认交叉构建 Windows amd64 和 macOS Apple Silicon 制品并生成校验和。
On macOS/Linux, Make builds Windows amd64 and macOS Apple Silicon packages plus checksums by default.

```bash
make build          # 全部制品 + SHA256SUMS / all artifacts + SHA256SUMS
make windows        # 仅 Windows / Windows only
make macos          # 仅 macOS / macOS only
make clean          # 清理制品 / clean artifacts
make dist-clean     # 删除 release 目录 / remove release dir
```

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

## 命令行参数 / Command-line Flags

```text
-listen string   监听地址 / listen address（默认 / default ":8080"）
-dir string      文件存储目录 / storage directory（中文系统 / Chinese OS "共享文件夹-lancopy"，其他 / other "shared-folder-lancopy"）
-max-gb int      单次上传总大小上限 GiB / max upload size per batch in GiB（默认 / default 10）
```

示例 / Example：

```bash
./lan-copy -listen :9000 -dir "$HOME/Downloads/LanCopy" -max-gb 20
```

上传资源限制可在设置 → 上传中由运行 Lan Copy 的本机修改，也可编辑共享目录同级应用数据目录中的 `upload-resources.json` 并重启服务。默认配置为 3 个并发上传、20 GiB 全局暂存空间和 60 秒读取空闲超时；范围分别为 1–32、1–1024 GiB、5–600 秒。配置文件只在网页保存后创建。单次上传总大小仍由 `-max-gb` 控制。目录上传先通过清单预检返回冲突，再传输文件；预检会话 15 分钟过期，最终提交仍会检查冲突。

## Windows 控制台行为 / Windows Console Behavior

中文：从资源管理器双击启动时显示启动信息与访问地址，自动打开默认浏览器，并提示“按任意键关闭本窗口”；按键后调用 `FreeConsole()` 关闭控制台，服务继续在后台运行。停止服务请从本机 `localhost` 页面操作（`/api/shutdown`）；若有上传或下载在途，需查看风险提示并输入“强制关闭”确认。重复启动时，无论双击还是从终端启动，都会探测已有实例并打开其浏览器页面，随后退出第二个进程，不启动第二份服务。`/healthz` 必须返回 `X-Lan-Copy-Service: lan-copy` 和 `ok`；并发首次启动时会短暂重试，端口被其他程序占用则报告错误。旧版本没有身份响应头，升级时需先关闭旧服务。

English: when launched by double-clicking in Explorer, the console prints startup info, opens the default browser, and waits for any key; the key press calls `FreeConsole()` so the console closes while the service keeps running. Shut it down from the local `localhost` page (`/api/shutdown`); if uploads or downloads are active, review the warning and type “强制关闭” to confirm. Repeated launches from Explorer or a terminal probe the occupied address, verify the Lan Copy health marker, open the existing page, and exit without starting another server. Probes briefly retry during concurrent startup. Stop older versions once when upgrading: they lack the service identity header.

实现位于 `internal/desktop/console_windows.go`（`console_other.go` 为空实现）。仅当进程独占控制台（`GetConsoleProcessList` 只有一个进程，即双击场景）时启用控制台关闭交互；重复启动识别不受此限制。浏览器、文件和目录均通过 Windows 原生 `ShellExecuteW` 打开。

The logic lives in `internal/desktop/console_windows.go` (`console_other.go` is a stub). Console detachment only activates when the process exclusively owns its console. Repeat-launch detection is independent of console ownership. Windows opens browsers, files and folders through ShellExecuteW.

## 架构说明 / Architecture

- 单二进制：根目录将 `web/` 通过 `go:embed` 内嵌并传给 `internal/app`；`app` 组装组件和 `internal/httpserver`，后者在 `net/http.ServeMux` 注册路由。`filestore`、`textstore`、`updater` 和 `desktop` 不依赖 HTTP 层。
- API 一览：
  - `GET /api/files` 列表（文件与目录列表以 previewType 表示预览能力，不支持的格式为空字符串）。目录上传先用 `POST /api/upload-tree/preflight?path=...` 提交 JSON 清单（`entries: [{path, directory}]`），响应包含一次性会话和冲突列表；确认后使用 `POST /api/upload-tree?path=...&session=...` 以 multipart 提交，首项为 `manifest` JSON（`entries`、`replace`），文件体字段为对应下标的 `file-N`。会话 15 分钟过期，取消时可调用 `DELETE /api/upload-tree/session/{id}`。旧版 `POST /api/upload` 已移除。相对路径逐段使用共用规则校验，拒绝越界、内部保留名前缀（不区分大小写）、Windows 保留设备名、非法字符、文件/目录冲突和清单内大小写别名；最多 10000 项（含隐含父目录）、64 个路径组件、2 MiB 清单，请求总字节数仍受 `-max-gb` 限制。内容流式写入隐藏的 `.upload-txn-*` 事务目录；并发上传、全部暂存文件字节和请求体空闲时间分别受设置页配置约束。完成后在锁内以同文件系统改名提交，并重新校验目标。普通文件同名时自动生成编号名称；目录冲突需确认后整体替换。事务记录、暂存内容与替换备份同处一个目录，进程重启时回滚未完成提交。拖拽使用目录 API 循环读取所有分页并保留空目录，目录选择按钮受浏览器能力限制，可能不包含空目录
  - `GET/PUT /api/settings/upload-resources` 读取或修改上传全局资源限制；访客只读，PUT 仅接受本机同源请求并要求 `X-Lan-Copy-Action: upload-resources`。配置含 `concurrentUploads`、`stagingGiB`、`idleTimeoutSeconds`。
  - `GET /thumbnails/{name}` 图片缩略图：JPEG/PNG/GIF 按比例缩至最长边 128px，限制源文件大小、像素数和并发解码；其他图片格式使用浏览器图片展示。缩略图 URL 携带修改时间和大小以更新浏览器缓存
  - `GET /preview-files/{name}` 预览资源（类型白名单、固定 MIME、inline、Range/HEAD；SVG 以 image/svg+xml 提供并用 sandbox CSP 隔离）；`/preview.html?path=...` 为预览页，文本最多读取前 256 KiB。PDF 使用内嵌 PDF.js 6.4.299 单页渲染，支持翻页和缩放。DOCX 由浏览器端 `docx-preview` 转成 HTML 预览，限制源文件 32 MiB，禁用嵌入 HTML 部件、评论和修订；不支持旧版 `.doc` 预览，仍可下载原文件。XLS/XLSX 由 Web Worker 内的 SheetJS 0.20.3 解析，单文件限制 32 MiB，每张工作表最多显示 250 行、50 列，最多显示 20 个工作表；公式显示缓存值，宏不执行。DOCX、SheetJS 预览资源版本、来源、完整性和许可证分别见 `web/vendor/docx-preview/README.md`、`web/vendor/sheetjs/README.md`；PDF.js 资源见 `web/vendor/pdfjs/README.md`。
  - `GET /files/{name}` 下载、`DELETE /api/files/{name}` 删除（嵌套路径作为整体 URL 编码；列表递归包含子文件夹中的文件）
  - `GET /api/filesystem?path=...&q=...` 读取目录（空路径为共享根目录）；指定 q 时递归搜索当前目录和子目录中的文件、文件夹名称，单次最多遍历 10000 项；客户端取消请求会中止后续遍历
  - `POST /api/filesystem/{mkdir|move|rename|delete}` 目录操作，JSON 字段为 `path`、`destination`、`name`；不覆盖同名目标，禁止越界路径和符号链接，非空目录删除先返回 409 和 requiresConfirmation=true，经二次确认后以 recursive=true 重试递归删除，根目录不可删除

  - `/` 共享空间首页，`/transfer.html` 文本共享页面（文本发布、复制和删除；文件上传与浏览统一在共享空间），旧地址 `/filesystem.html` 重定向到首页；文件夹对应存储目录中的真实目录，无额外索引数据库
  - `GET/POST /api/texts`、`DELETE /api/texts/{id}` 共享文本保存在共享目录同级的 `.lan-copy-app-data-<共享目录名>/` 中。旧版本在共享目录内留下的文本库和上传事务会在启动时迁入该目录。
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

GitHub Actions 已支持推送 `main` 自动构建、推送版本标签后发布 Release；一次性签名配置及版本规则见 [GitHub 自动构建与发布](GITHUB-ACTIONS.md)。CI 使用已提交版本，不执行自动递增。

GitHub Actions builds on pushes to `main` and publishes releases on matching version tags. See [automated builds and releases](GITHUB-ACTIONS.md) for signing configuration and version rules. CI uses the committed version without incrementing it.

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
