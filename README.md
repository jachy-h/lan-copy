# Lan Copy

简体中文 | [English](README.en.md)

在一台电脑上启动，同一局域网内的手机、平板和电脑就能通过浏览器共享文件与文字。无需登录，无需网盘。

## 适合什么时候用

- 手机与电脑互传照片、视频和文件。
- 同一网络内分享文件夹或大文件。
- 跨设备复制链接、文字和临时笔记。

## 开始使用

**macOS（Apple Silicon）可以用 Homebrew 安装**，跳过手动解压：

```bash
brew install jachy-h/jachy-h/lancopy
lancopy                       # 打印启动日志后在后台运行，随后打开 http://localhost:8080
```

`lancopy` 会打印本机与局域网地址，然后把服务转入后台并恢复终端；再次执行会提示已在运行。日志写入 `~/Library/Logs/lancopy/lancopy.log`，可用 `LAN_COPY_LOG_FILE` 指定其他位置。停止服务请点击页面右上角的**关闭软件**；需要 launchd 常驻时仍可用 `brew services start lancopy`。带参数运行（例如 `lancopy -dir ~/共享`）保持前台，`Ctrl-C` 停止。

Homebrew 安装后命令名为 `lancopy`，请用 `brew upgrade lancopy` 升级；应用内更新会改写 Homebrew 管理的文件，不建议使用。暂不支持 Intel Mac。

1. 到 [下载页面](https://github.com/jachy-h/lan-copy/releases/latest) 获取对应系统的压缩包并解压。目前提供 Windows x64 和 macOS Apple Silicon 版本。
2. 启动程序：
   - **Windows**：双击 `lan-copy.exe`，会自动打开浏览器。
   - **macOS**：在终端进入解压目录，执行 `./lan-copy`（前台运行，`Ctrl-C` 停止），再打开 <http://localhost:8080>。
3. 点击页面顶部的**分享图标**，让同一 Wi-Fi / 局域网内的其他设备扫码，或在浏览器打开其中的局域网地址。
4. 在**内网空间**拖入文件或文件夹；在**碎片快传**发布文字或粘贴、拖拽图片，也可以把碎片存入内网空间的指定文件夹。碎片默认 7 天到期，可在**设置 → 通用**中调整。

使用结束后，点击右上角的**关闭软件**。Windows 控制台可按任意键关闭，服务仍会运行。

**开机自启动**：在运行 Lan Copy 的电脑上，打开齿轮图标 → **通用** → **开机自启动**。支持 Windows 和 macOS，默认关闭；开启后从下次登录起在后台启动，不打开浏览器。关闭软件不会取消自启动。开启后请保留程序与共享目录的位置，移动后需重新开启；如被系统禁用，请在系统的启动应用 / 登录项中允许。

文件和文字保存在运行 Lan Copy 的电脑上。默认共享目录名为 `共享文件夹-lancopy`（中文系统）或 `shared-folder-lancopy`（其他系统）。

**打印（Windows 主机）**：点击分享按钮左侧的打印图标，选择主机已安装的打印机，再选择本地 PDF、JPG/JPEG 或 PNG。可翻页、放大预览，确认后发送到主机打印；也可在内网空间中点击支持文件右侧的 **⋯ → 打印**，自动进入该文件的打印预览；点击文件名仍然打开普通预览。也可先打印测试页。当前按主机默认纸张与方向打印，固定 1 份、单面；单个文件最多 50 MiB，PDF 最多 50 页。任务“已提交”表示进入 Windows 队列，实际出纸请在设备处确认。

**智能表格**：共享目录中的 CSV 默认仍是普通文件；直接点击 CSV/TSV 文件名会以只读表格预览，首行为表头，表格与原始文本可在工具栏切换。需要编辑、查询或插件读写时，在内网空间的文件行菜单中选择“标记为智能表格”，文件会显示“表格”标记，点击即可浏览、搜索、排序和编辑。搜索框会同时搜索全部列，输入后自动查询并高亮匹配文字。表格工具栏的“列设置”可以调整列的显示顺序、隐藏或显示指定列；偏好保存在 CSV 同目录的 `<表格文件名>.hidden.metadata.json` 中，只影响当前视图，不修改 CSV，也不改变插件 API 的列顺序和版本。该元数据文件不会出现在 Lan Copy 的文件列表、搜索、下载或预览中。插件通过 `LAN_COPY_API_URL`/`LAN_COPY_API_TOKEN` 调用查询与 `lock_and_update` 原子更新，使用版本校验避免并发覆盖。

**插件服务（局域网可管理）**：在“设置 → 插件”中点击“在新标签页打开插件服务”，即可通过 Docker 或 Podman 导入、安装、启动、停止和查看本地插件，上传 `docker save` / `podman save` 生成的镜像包即可。插件以受限容器运行，服务本身对局域网可达；任何能访问 Lan Copy 的设备都可管理插件，请只安装可信镜像。启动时会自动检测可用的 Docker 或 Podman（默认优先 Docker），运行时不可用时可在插件页点击“启动 Podman”。需要运行 Lan Copy 的电脑已安装其中之一。插件可在 Manifest 中订阅上传、文件夹/文件操作和文本变化等生命周期事件，“对接”菜单展示事件目录、投递状态、测试事件和智能表格 API 接入方式。Manifest 声明 `ui` 的插件会自动获得局域网可访问的界面入口 `http://<lan-copy>/plugin-app/<插件ID>/`，插件按 `X-Forwarded-Prefix` 适配子路径即可。需要在上传后弹窗确认的插件可声明 `uploadHook`：上传成功后 Lan Copy 调用插件 Hook，并把插件返回的页面显示在同源弹窗中；Hook 失败或超时不会影响上传。协议、镜像构建与安全边界见 [插件服务协议](docs/PLUGIN-PROTOCOL.md)。

**其他设备打不开？** 确认连接同一局域网，并允许 Lan Copy 通过系统防火墙；访客 Wi-Fi 可能隔离设备。

**插件连不上 Lan Copy？** Windows/macOS 上的 Podman 运行在 `podman machine` 虚拟机里，容器中的 `host.containers.internal` 指向虚拟机而不是宿主机。Lan Copy 会自动改用宿主局域网 IPv4，并在“设置 → 插件”顶部只读显示“插件 API 地址”和选择理由。若地址不正确，请确认宿主机防火墙放行监听端口，或用 `-plugin-api-url http://<可达地址>:<端口>` 显式指定；修改后重启插件（会重建容器）或重新安装，使新地址注入容器。Linux 原生 Podman 和 Docker 不受影响。

**更新软件**：打开齿轮图标 → **更新**，自动检查 GitHub 最新正式版；发现新版后，本机用户和局域网访客均可选择 **下载并更新**，更新运行 Lan Copy 的电脑；也可选择 **前往查看**。还可手动下载对应系统的新版发布包，拖入完整压缩包。签名验证通过后会自动安装、重启并刷新页面，保留共享内容。请先完成其他文件传输。

> 仅在可信局域网使用。默认没有账号密码，局域网内其他人可以读取、修改、删除共享内容，也可以发起程序更新。不要暴露到公网。

---

## 面向开发者

**普通使用者可忽略以下全部内容。下载发布包即可使用，无需安装 Go、构建程序或生成密钥。**

### 本地开发

需要 Go 1.22 或更新版本。

```bash
git clone https://github.com/jachy-h/lan-copy.git
cd lan-copy
go run .
```

打开 <http://localhost:8080>。源码运行不支持页面更新。

前后端联调时，在项目根目录运行 `air` 启动后端，再在另一终端运行 `make frontend-dev`，访问 Vite 输出的地址。Air 通过仓库 `.air.toml` 传入 `-air-dev`，此时后端 `/` 只显示前端调试启动提示；API、vendor 和 logo 资源仍可用于 Vite 代理。普通 `go run .`、`go build .` 和发布制品继续提供完整内嵌前端。


### 构建与发布

需要 Go、Make 和 Python 3。首次构建前，按[更新发布说明](docs/UPDATING.md#固定发布签名公钥)生成 `.config/update.key` 和 `.config/update.pub`；后续版本复用同一套密钥，私钥不要提交或分发。

```bash
make test                    # 运行构建脚本与 Go 测试
make build                   # 递增构建号，例如 0.8.1-build3 → 0.8.1-build4
make build VERSION=0.8.2     # 升级基础版本，首次构建为 0.8.2-build1
make publish                 # 干净的 main 分支：递增构建号、提交版本、打标签并推送发布
make deploy                  # 构建并部署到默认目标（自动选择平台包）
```

版本格式为 `x.y.z-build{c}`，例如 `0.8.1-build3` 表示 `0.8.1` 的第 3 次构建；文件不存在时从 `0.1.0-build1` 开始。每次 `make build` 在成功打包后把构建号 `+1` 写回 `VERSION`，失败时不改版本；基础版本升级（例如 `0.8.1` → `0.8.2`）时构建号归零，首次构建为 `0.8.2-build1`。显式 `VERSION=x.y.z-build{c}` 会原样使用，CI 和重跑依赖这一行为。分发 `release/` 中对应系统的压缩包。`VERSION` 应随发布代码提交，`.config/` 和 `release/` 已被 Git 忽略。

`make publish` 要求先提交所有代码，并输入 `y` 确认完整目标版本（`n` 取消）；默认在同一基础版本上递增构建号，并仅提交 `VERSION`，创建 `v0.8.1-build4` 形式的标签后推送到 `origin`，由 Actions 完成正式发布。升级基础版本使用 `make publish VERSION=0.9.0`，首次为 `0.9.0-build1`。

支持通过 `scripts/deploy.py` 将签名包上传到目标机器并确认重启结果，替代浏览器拖拽；配置和命令见[本地脚本自动部署](docs/UPDATING.md#本地脚本自动部署cd)。

推送 `main` 后，GitHub Actions 自动测试并构建签名包；推送与 `VERSION` 完全一致的 `vx.y.z-build{c}` 标签后发布 GitHub Release。首次需配置仓库签名密钥，见[自动构建与发布](docs/GITHUB-ACTIONS.md)。

- [开发者文档](docs/DEVELOPING.md)：架构、命令行参数、测试、Docker 和跨平台构建。
- [更新发布说明](docs/UPDATING.md)：签名密钥、版本管理、更新包格式和失败恢复。
- [Windows 安全与签名发布](docs/WINDOWS-SECURITY.md)：系统拦截提示及 Windows 代码签名。

## 许可证

[MIT](LICENSE)
