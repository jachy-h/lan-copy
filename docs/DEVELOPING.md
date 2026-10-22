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
├── main.go               # HTTP 服务、文件上传/下载 API / HTTP server, upload & download APIs
├── text.go               # 文本共享存储 / Shared-text storage
├── console_windows.go    # Windows 控制台交互（按任意键关闭）/ Windows console interaction
├── console_other.go      # 非 Windows 平台的空实现 / No-op for non-Windows
├── web/                  # 前端资源，go:embed 内嵌 / Frontend assets embedded via go:embed
├── lan-copy.syso         # 编译时自动链接的图标资源 / Icon resource linked by go build
├── build.bat             # Windows 快捷构建脚本 / Quick build script (Windows)
├── Makefile              # 全平台制品构建 / Cross-platform artifact builds
└── docs/DEVELOPING.md    # 本文档 / This document
```

## 环境要求 / Requirements

Go 1.22 或更新版本。运行程序只依赖 Go 标准库；使用 Make 打包还需要 Python 3。
Go 1.22+. The app uses only the Go standard library; Make packaging also requires Python 3.

## 本地运行与测试 / Run & Test

```bash
go run .            # 运行 / run
go test ./...       # 全部测试 / all tests
go vet ./...        # 静态检查 / static check
node scripts/test-upload-tree.cjs  # 目录遍历测试（Node.js）/ folder traversal tests
```

Windows 下也可使用 `build.bat` / On Windows you can also use `build.bat`：

| 命令 / Command | 作用 / Purpose |
|------|------|
| `build.bat build` | 构建到 `release\lan-copy.exe` / build to `release\lan-copy.exe` |
| `build.bat release` | 签名并校验 Windows 发布包 / build and verify signed Windows release packages |
| `build.bat test` | 运行测试 / run tests |
| `build.bat run` | 直接运行 / run directly |
| `build.bat clean` | 清理构建产物 / clean artifacts |

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
局域网快传-Windows-amd64.zip
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

## Windows 控制台行为 / Windows Console Behavior

中文：从资源管理器双击启动时显示启动信息与访问地址，自动打开默认浏览器，并提示“按任意键关闭本窗口”；按键后调用 `FreeConsole()` 关闭控制台，服务继续在后台运行。停止服务请用网页右上角“关闭软件”（`/api/shutdown`）。重复启动时，无论双击还是从终端启动，都会探测已有实例并打开其浏览器页面，随后退出第二个进程，不启动第二份服务。`/healthz` 必须返回 `X-Lan-Copy-Service: lan-copy` 和 `ok`；并发首次启动时会短暂重试，端口被其他程序占用则报告错误。旧版本没有身份响应头，升级时需先关闭旧服务。

English: when launched by double-clicking in Explorer, the console prints startup info, opens the default browser, and waits for any key; the key press calls `FreeConsole()` so the console closes while the service keeps running. Shut it down from the web page (“关闭软件”, `/api/shutdown`). Repeated launches from Explorer or a terminal probe the occupied address, verify the Lan Copy health marker, open the existing page, and exit without starting another server. Probes briefly retry during concurrent startup. Stop older versions once when upgrading: they lack the service identity header.

实现位于 `console_windows.go`（`console_other.go` 为空实现）。仅当进程独占控制台（`GetConsoleProcessList` 只有一个进程，即双击场景）时启用控制台关闭交互；重复启动识别不受此限制。浏览器、文件和目录均通过 Windows 原生 `ShellExecuteW` 打开。

The logic lives in `console_windows.go` (`console_other.go` is a stub). Console detachment only activates when the process exclusively owns its console. Repeat-launch detection is independent of console ownership. Windows opens browsers, files and folders through ShellExecuteW.

## 架构说明 / Architecture

- 单二进制：`web/` 通过 `go:embed` 内嵌；所有路由挂在 `net/http.ServeMux`。
- API 一览：
  - `GET /api/files` 列表（文件与目录列表以 previewType 表示预览能力，不支持的格式为空字符串）、`POST /api/upload?path=...` 流式上传（可选 path 指定目标文件夹）（multipart → 临时文件 → 原子改名）
  - `POST /api/upload-tree?path=...` 文件/目录批量上传：multipart 首项为 `manifest` JSON（`entries: [{path, directory}]`），文件体字段为对应下标的 `file-N`。相对路径逐段校验，拒绝越界、保留名、文件/目录冲突和大小写别名；最多 10000 项（含隐含父目录）、64 个路径组件、2 MiB 清单，请求总字节数仍受 `-max-gb` 限制。先流式写入隐藏的 `.upload-tree-*` 临时目录，完成后在锁内按顶层条目改名提交；同名目录整体加后缀，不合并；失败清理临时内容。拖拽使用目录 API 循环读取所有分页并保留空目录，目录选择按钮受浏览器能力限制，可能不包含空目录
  - `GET /thumbnails/{name}` 图片缩略图：JPEG/PNG/GIF 按比例缩至最长边 128px，限制源文件大小、像素数和并发解码；其他图片格式使用浏览器图片展示。缩略图 URL 携带修改时间和大小以更新浏览器缓存
  - `GET /preview-files/{name}` 预览资源（类型白名单、固定 MIME、inline、Range/HEAD；SVG 以 image/svg+xml 提供，CSP sandbox 禁用脚本和外部资源）；`/preview.html?path=...` 为预览页，文本最多读取前 256 KiB。PDF 使用内嵌 PDF.js 单页渲染、按需读取，支持翻页和缩放；第三方资源版本、来源、校验值和许可证见 `web/vendor/pdfjs/README.md`，更新时须一并更新库、worker、CMaps、字体和 WASM 资源
  - `GET /files/{name}` 下载、`DELETE /api/files/{name}` 删除（嵌套路径作为整体 URL 编码；列表递归包含子文件夹中的文件）
  - `GET /api/filesystem?path=...&q=...` 读取目录（空路径为共享根目录）；指定 q 时递归搜索当前目录和子目录中的文件、文件夹名称
  - `POST /api/filesystem/{mkdir|move|rename|delete}` 目录操作，JSON 字段为 `path`、`destination`、`name`；不覆盖同名目标，禁止越界路径和符号链接，非空目录删除先返回 409 和 requiresConfirmation=true，经二次确认后以 recursive=true 重试递归删除，根目录不可删除
  - `/` 共享空间首页，`/transfer.html` 文本共享页面（文本发布、复制和删除；文件上传与浏览统一在共享空间），旧地址 `/filesystem.html` 重定向到首页；文件夹对应存储目录中的真实目录，无额外索引数据库
  - `GET/POST /api/texts`、`DELETE /api/texts/{id}` 共享文本
  - `GET /api/events` SSE 实时刷新（25s 心跳）、`GET /api/access` 局域网地址探测
  - `POST /api/shutdown` 网页关闭服务、`GET /healthz` 健康检查
- 并发模型：文件事件通过订阅 channel 广播；上传先写 `.upload-*` 临时名再在互斥锁下原子改名，实现同名覆盖保护。

Single binary: `web/` is embedded via `go:embed`; routes are registered on `net/http.ServeMux`. File events fan out through subscriber channels; uploads stream to a `.upload-*` temp file then atomically rename under a mutex for overwrite protection.

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

## 应用更新 / Application updates

设置弹窗、更新包格式、签名和进程恢复机制见 [UPDATING.md](UPDATING.md)。新增接口：`GET /api/app`（版本、平台、更新可用性）和 `POST /api/update`（本机原始压缩包上传，要求 `X-Lan-Copy-Action: update`）。运行 `go test ./...` 会实际构建、更新和恢复测试进程；`go test -short ./...` 跳过进程生命周期测试。

## 开机自启动 / Start at Login

设置 → 通用中的开关通过本机限定的 `GET/PUT /api/settings/autostart` 读写系统启动项。PUT 要求 `X-Lan-Copy-Action: autostart`，并校验同源请求。开关默认关闭，只影响下一次用户登录；关闭当前服务不会移除启动项。
The General settings switch uses the local-only `GET/PUT /api/settings/autostart` endpoint. PUT requires the action header and same-origin validation. It is off by default, applies at the next user login, and survives quitting the service.

- macOS：`~/Library/LaunchAgents/com.lan-copy.autostart.plist`，使用 `RunAtLoad`，不配置自动重启，不在切换开关时加载或卸载当前任务。
- Windows：当前用户的 `Software\Microsoft\Windows\CurrentVersion\Run` 中的 `LanCopy` 值；完整参数保存在 `%APPDATA%\LanCopy\startup.json`，避免 Run 命令的长度限制。
- 自启动保存绝对程序路径、工作目录、共享目录、监听地址和上传限制。`--autostart` 跳过浏览器与控制台交互，`--working-dir` 恢复工作目录；这些内部参数会在更新重启后保留。
- 开发运行、其他系统不提供该功能；可用 `LAN_COPY_DISABLE_AUTOSTART=1` 显式禁用。系统设置可能额外阻止启动，网页显示注册状态并提示用户在系统设置中允许。

Registration uses a per-user macOS LaunchAgent or the Windows Run key. Absolute paths and launch options are preserved, and startup does not open a browser. Updates preserve the internal startup flags. Development runs and unsupported systems disable the feature; `LAN_COPY_DISABLE_AUTOSTART=1` disables it explicitly. The UI reports registration state; OS startup/login settings can separately block execution.
