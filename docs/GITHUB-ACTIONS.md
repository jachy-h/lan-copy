# GitHub 自动构建与发布

工作流：`.github/workflows/release.yml`。

| 操作 | 结果 |
| --- | --- |
| 推送 `main` | 在 Linux、Windows、macOS 上测试，通过后交叉构建带更新签名的 Windows amd64 和 macOS Apple Silicon 发布包 |
| 推送 `v版本号` 标签 | 完成相同的测试与构建，并发布 GitHub Release |
| 在 Actions 手动运行 | 构建所选分支；选择版本标签时也会进入发布流程 |

普通分支推送不会发布正式 Release。`main` 的构建产物可在对应 Actions 运行的 **Artifacts** 中下载，保留 14 天；包含两个压缩包和 `SHA256SUMS`。正式标签会将相同的三个文件上传为 Release 附件。

## 一次性配置签名

在仓库 **Settings → Secrets and variables → Actions** 中配置：

| 类型 | 名称 | 值 |
| --- | --- | --- |
| Repository secret | `LAN_COPY_SIGNING_KEY` | 现有 `.config/update.key` 文件的完整内容 |
| Repository variable | `LAN_COPY_UPDATE_PUBLIC_KEY` | 现有 `.config/update.pub` 文件的完整内容 |

复用现有密钥，才能让已经分发的程序接受 CI 发布的更新。私钥不写入工作流、仓库或产物；只在签名构建步骤通过环境变量注入。缺少配置或密钥不匹配时构建失败，不生成无签名发布包。

使用已登录的 GitHub CLI 也可配置（以下命令会将密钥上传至指定 GitHub 仓库，仅由仓库维护者执行）：

```bash
gh secret set LAN_COPY_SIGNING_KEY --repo jachy-h/lan-copy < .config/update.key
gh variable set LAN_COPY_UPDATE_PUBLIC_KEY --repo jachy-h/lan-copy < .config/update.pub
```

GitHub 的更新包签名是 Ed25519 签名，不包含 Windows Authenticode 代码签名或 macOS 签名公证。Windows 系统代码签名仍按 [WINDOWS-SECURITY.md](WINDOWS-SECURITY.md) 单独处理。

## 版本与发布

本地 `make build` 仍会递增 patch。CI 使用已提交的 `VERSION`，不递增、不自动提交，也不推送版本改动。这样分支构建、标签构建及失败重跑都使用同一个版本。

1. 本地确认 `VERSION` 是要发布的版本；如需递增，可执行 `make build`。
2. 提交代码和 `VERSION`，推送 `main`。等待 Actions 构建成功，可先下载 Artifacts 验证。
3. 给对应提交添加同名版本标签并推送：

   ```bash
   git tag -a "v$(cat VERSION)" -m "Lan Copy $(cat VERSION)"
   git push origin "v$(cat VERSION)"
   ```

标签必须严格等于该提交的 `v` + `VERSION`，例如 `VERSION` 为 `0.1.4` 时，标签必须是 `v0.1.4`。标签不匹配会提前失败，防止附件版本与 Release 名称不一致。

发布 job 先核对下载包的校验和，再创建草稿、上传全部附件，最后公开 Release；上传失败时保留草稿以便重跑。已公开的 Release 不会在重跑时覆盖。只有发布 job 拥有 `contents: write` 权限，测试与构建 job 使用只读权限。

## 工作流启用

提交并推送工作流及相关脚本后生效。首次远端运行才能确认 GitHub runner 和仓库权限配置；本地检查不能替代一次实际 Actions 运行。

官方参考：[push 触发规则](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#push)、[GitHub CLI Release 命令](https://cli.github.com/manual/gh_release_create)。
