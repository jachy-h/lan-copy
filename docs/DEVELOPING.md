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

Go 1.22 或更新版本。零第三方依赖，仅使用 Go 标准库。
Go 1.22+. Zero third-party dependencies, standard library only.

## 本地运行与测试 / Run & Test

```bash
go run .            # 运行 / run
go test ./...       # 全部测试 / all tests
go vet ./...        # 静态检查 / static check
```

Windows 下也可使用 `build.bat` / On Windows you can also use `build.bat`：

| 命令 / Command | 作用 / Purpose |
|------|------|
| `build.bat build` | 构建到 `release\lan-copy.exe` / build to `release\lan-copy.exe` |
| `build.bat test` | 运行测试 / run tests |
| `build.bat run` | 直接运行 / run directly |
| `build.bat clean` | 清理构建产物 / clean artifacts |

## 构建制品 / Building Artifacts

在 macOS 或 Linux 上用 Make 交叉构建全部平台制品并生成校验和。
On macOS/Linux use Make to cross-build every platform artifact plus checksums.

```bash
make build          # 全部制品 + SHA256SUMS / all artifacts + SHA256SUMS
make windows        # 仅 Windows / Windows only
make macos          # 仅 macOS / macOS only
make clean          # 清理制品 / clean artifacts
make dist-clean     # 删除 release 目录 / remove release dir
```

输出保存在 `release/` / Output lands in `release/`：

```text
lan-copy-windows-amd64.zip
lan-copy-windows-arm64.zip
lan-copy-macos-amd64.tar.gz
lan-copy-macos-arm64.tar.gz
SHA256SUMS
```

压缩包内含可执行文件与 README；Windows 版为带图标的控制台程序（未用 `-H=windowsgui`）。
Archives contain the binary and README; the Windows binary is a console app with an embedded icon (no `-H=windowsgui`).

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
-dir string      文件存储目录 / storage directory（默认 / default "./lan-copy-data"）
-max-gb int      单次上传总大小上限 GiB / max upload size per batch in GiB（默认 / default 10）
```

示例 / Example：

```bash
./lan-copy -listen :9000 -dir "$HOME/Downloads/LanCopy" -max-gb 20
```

## Windows 控制台行为 / Windows Console Behavior

中文：双击启动时显示启动信息与访问地址，提示“按任意键关闭本窗口”；按键后调用 `FreeConsole()` 关闭控制台，服务继续在后台运行。停止服务请用网页右上角“关闭软件”（`/api/shutdown`）。若端口已被占用且 `/healthz` 探测到本程序实例，则显示同样的提示界面后退出。

English: on double-click launch, the console prints startup info and waits for any key; the key press calls `FreeConsole()` so the console closes while the service keeps running. Shut it down from the web page (“关闭软件”, `/api/shutdown`). If the port is taken but `/healthz` identifies a running instance of this program, the same screen is shown and the second process exits.

实现位于 `console_windows.go`（`console_other.go` 为空实现）。仅当进程独占控制台（`GetConsoleProcessList` 只有一个进程，即双击场景）时启用交互；从终端或重定向 stdin 启动时保持正常服务器行为。

The logic lives in `console_windows.go` (`console_other.go` is a stub). The interaction only activates when the process exclusively owns its console (`GetConsoleProcessList` returns one PID — the double-click case); terminal or redirected-stdin launches behave like a normal server.

## 架构说明 / Architecture

- 单二进制：`web/` 通过 `go:embed` 内嵌；所有路由挂在 `net/http.ServeMux`。
- API 一览：
  - `GET /api/files` 列表、`POST /api/upload` 流式上传（multipart → 临时文件 → 原子改名）
  - `GET /files/{name}` 下载、`DELETE /api/files/{name}` 删除
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

1. 更新两个 README 与本文档 / update both READMEs and this doc
2. `go vet ./... && go test ./...` 全部通过 / all green
3. `make build` 生成 `release/` 制品与 `SHA256SUMS`
4. 在 GitHub Releases 上传全部制品并发布 / upload all artifacts to GitHub Releases and publish
