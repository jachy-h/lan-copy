# Lan Copy

简体中文 | [English](README.en.md)

在一台电脑上启动，同一局域网内的手机、平板和电脑就能通过浏览器共享文件与文字。无需登录，无需网盘。

## 适合什么时候用

- 手机与电脑互传照片、视频和文件。
- 同一网络内分享文件夹或大文件。
- 跨设备复制链接、文字和临时笔记。

## 开始使用

1. 到 [下载页面](https://github.com/jachy-h/lan-copy/releases/latest) 获取对应系统的压缩包并解压。目前提供 Windows x64 和 macOS Apple Silicon 版本。
2. 启动程序：
   - **Windows**：双击 `lan-copy.exe`，会自动打开浏览器。
   - **macOS**：在终端进入解压目录，执行 `./lan-copy`，再打开 <http://localhost:8080>。
3. 点击页面顶部的**分享图标**，让同一 Wi-Fi / 局域网内的其他设备扫码，或在浏览器打开其中的局域网地址。
4. 在**共享空间**拖入文件或文件夹；在**文本快传**发布、复制文字。

使用结束后，点击右上角的**关闭软件**。Windows 控制台可按任意键关闭，服务仍会运行。

**开机自启动**：在运行 Lan Copy 的电脑上，打开齿轮图标 → **通用** → **开机自启动**。支持 Windows 和 macOS，默认关闭；开启后从下次登录起在后台启动，不打开浏览器。关闭软件不会取消自启动。开启后请保留程序与共享目录的位置，移动后需重新开启；如被系统禁用，请在系统的启动应用 / 登录项中允许。

文件和文字保存在运行 Lan Copy 的电脑上。默认共享目录名为 `共享文件夹-lancopy`（中文系统）或 `shared-folder-lancopy`（其他系统）。

**其他设备打不开？** 确认连接同一局域网，并允许 Lan Copy 通过系统防火墙；访客 Wi-Fi 可能隔离设备。

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

### 构建与发布

需要 Go、Make 和 Python 3。首次构建前，按[更新发布说明](docs/UPDATING.md#固定发布签名公钥)生成 `.config/update.key` 和 `.config/update.pub`；后续版本复用同一套密钥，私钥不要提交或分发。

```bash
make test                    # 运行构建脚本与 Go 测试
make build                   # 自动签名、递增 patch 版本并打印构建信息
make build VERSION=1.2.3     # 也可指定版本
make publish                 # 干净的 main 分支：构建、提交版本、打标签并推送发布
```

构建从 `VERSION` 读取基线，全部成功后才保存新版本；分发 `release/` 中对应系统的压缩包。`VERSION` 应随发布代码提交，`.config/` 和 `release/` 已被 Git 忽略。

`make publish` 要求先提交所有代码，并输入 `y` 确认目标版本（`n` 取消），自动递增版本并仅提交 `VERSION`，创建版本标签后推送到 `origin`，由 Actions 完成正式发布。

推送 `main` 后，GitHub Actions 自动测试并构建签名包；推送与 `VERSION` 一致的 `v版本号` 标签后发布 GitHub Release。首次需配置仓库签名密钥，见[自动构建与发布](docs/GITHUB-ACTIONS.md)。

- [开发者文档](docs/DEVELOPING.md)：架构、命令行参数、测试、Docker 和跨平台构建。
- [更新发布说明](docs/UPDATING.md)：签名密钥、版本管理、更新包格式和失败恢复。
- [Windows 安全与签名发布](docs/WINDOWS-SECURITY.md)：系统拦截提示及 Windows 代码签名。
