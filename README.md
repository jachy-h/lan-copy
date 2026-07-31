# Lan Copy

一个极小、零依赖的局域网快传工具。只需在一台电脑运行，家中同一 Wi-Fi / 局域网内的其他设备打开网页，即可传递文件或共享一段文字。

- **文件快传**：拖拽上传、批量传输、下载和删除文件
- **文本共享**：传递链接、验证码、命令或临时笔记，一键复制
- **单文件部署**：Go 编译出的一个可执行文件，网页资源已内嵌
- **零客户端**：电脑、手机、平板有浏览器就能用
- **扫码访问**：自动检测可达的局域网地址并生成二维码，手机一扫即开
- **实时同步**：文件上传或删除后，所有已打开的客户端立即刷新
- **本机操作**：服务所在电脑可从网页直接打开文件或所在目录
- **移动适配**：适配安全区域、触控尺寸和窄屏布局，避免误缩放
- **传大文件**：上传采用流式写盘，不会把整个文件载入内存
- **覆盖保护**：同名文件自动保存为 `文件 (2).ext`
- **网页关闭**：可在页面右上角确认并关闭服务
- **零外部依赖**：仅使用 Go 标准库，无数据库

## 快速开始

需要 Go 1.22 或更新版本：

```bash
go run .
```

启动后终端会显示地址，例如：

```text
局域网访问: http://192.168.1.23:8080
```

在另一台电脑的浏览器打开这个地址：可以直接拖放文件，也可以切换到“共享文本”发布文字。文本和文件都会持久化保存在运行 Lan Copy 的电脑上。

> 如果另一台设备打不开，请允许 `lan-copy` 通过系统防火墙，并确认两台设备连接的是同一个局域网（访客 Wi-Fi 通常会隔离设备）。

## 使用 Make 构建制品

在 macOS 或 Linux 上运行以下命令，可以交叉构建 Windows 与 macOS 的 x64、ARM64 制品：

```bash
# 构建全部 Windows 和 macOS 制品，并生成 SHA-256 校验文件
make build

# 也可以按平台单独构建
make windows
make macos
```

构建结果保存在 `dist/`：

```text
lan-copy-windows-amd64.zip
lan-copy-windows-arm64.zip
lan-copy-macos-amd64.tar.gz
lan-copy-macos-arm64.tar.gz
SHA256SUMS
```

Windows 压缩包同时包含静默版 `lan-copy.exe` 和控制台版 `lan-copy-console.exe`。使用 `make clean` 可清理上述制品，`make test` 可运行测试。

## 编译成极小单文件

macOS / Linux：

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o lan-copy .
./lan-copy
```

Windows PowerShell：

```powershell
$env:CGO_ENABLED=0
# 静默版：双击后不显示控制台，通过网页右上角的“关闭软件”退出
go build -trimpath -ldflags="-s -w -H=windowsgui" -o lan-copy.exe .
# 控制台版：用于查看启动地址或排查错误
go build -trimpath -ldflags="-s -w" -o lan-copy-console.exe .
```

运行静默版后打开 <http://localhost:8080>。关闭控制台版的窗口会同时退出程序。

也可以交叉编译：

```bash
# Windows x64 静默版
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -H=windowsgui" -o lan-copy.exe .
# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o lan-copy-macos-arm64 .
```

## Docker

```bash
docker build -t lan-copy .
docker run --rm -p 8080:8080 -v "$PWD/lan-copy-data:/data" lan-copy
```

## 参数

```text
-listen string   监听地址（默认 ":8080"）
-dir string      文件存储目录（默认 "./lan-copy-data"）
-max-gb int      单次上传总大小上限 GiB（默认 10）
```

示例：使用 9000 端口，并把文件保存到下载目录：

```bash
./lan-copy -listen :9000 -dir "$HOME/Downloads/LanCopy" -max-gb 20
```

## 安全说明

Lan Copy 面向可信的家庭局域网，默认不设账号密码。局域网里的任何设备都可以查看、复制、删除共享内容，也可以通过网页关闭服务。请勿直接暴露到公网；在公司、校园或公共 Wi-Fi 中使用时也应谨慎。
