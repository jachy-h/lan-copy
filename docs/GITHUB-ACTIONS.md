# GitHub 自动构建与发布

工作流：`.github/workflows/release.yml`。

| 操作 | 结果 |
| --- | --- |
| 推送 `main` | 在 Linux、Windows、macOS 上测试，通过后交叉构建带更新签名的 Windows amd64 和 macOS Apple Silicon 发布包 |
| 推送 `v版本号` 标签 | 完成相同的测试与构建，并发布 GitHub Release |
| 在 Actions 手动运行 | 构建所选分支；选择版本标签时也会进入发布流程 |

普通分支推送不会发布正式 Release。`main` 的构建产物可在对应 Actions 运行的 **Artifacts** 中下载，保留 14 天；包含两个压缩包和 `SHA256SUMS`。正式标签会将相同的三个文件上传为 Release 附件。

构建后会重新读取两个真实发布包，验证 Ed25519 签名、程序哈希、平台、包内版本与 `VERSION` 一致，并验证附件名称能被在线更新器识别；检查通过才上传产物。发布时保留固定附件名 `Lan-Copy-Windows-amd64.zip` 和 `Lan-Copy-macOS-Apple-Silicon.tar.gz`，标签使用 `v` + `VERSION`。全部附件上传完成后才公开并标记为 Latest，供客户端“更新”菜单检查和下载。

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

## 一键发布：make publish

本地代码全部提交后，在 `main` 分支执行：

```bash
make publish
```

构建前会提示 `请确认是否发布 x.y.z 版本 [y/n]:`。输入 `y` 后继续；输入 `n`、按 Ctrl+C 或关闭输入会取消，不构建、不修改版本、不提交、不创建标签或推送。其他输入会重新提示。

命令会依次完成：

1. 检查暂存区、工作区和未跟踪文件；有未提交改动就提示先 `git commit` 并停止。已忽略的 `.config/` 和 `release/` 不算未提交改动。
2. 检查当前分支为 `main`、远端可访问、没有落后或分叉，以及目标版本标签在本地和远端都不存在。
3. 调用签名构建，默认递增 patch；也可用 `make publish VERSION=1.2.3` 指定更高版本。
4. 构建成功后，仅将生成的 `VERSION` 提交为 `chore: release v版本号`，再创建同名附注标签。构建期间出现其他改动会停止，不将其带入版本提交。
5. 将发布提交和版本标签通过一次 `git push --atomic` 推送至 `origin`，两者全部成功或全部拒绝。可通过 `PUBLISH_REMOTE` 指定其他远端。
6. GitHub Actions 在远端重新测试、构建签名包，全部通过后公开 Release。命令在推送后返回，最终发布状态在 Actions 页面查看。

该命令会实际创建 Git 提交、标签并推送。可以先执行 `make -n publish` 查看计划，预览不会构建或修改版本、提交、标签及远端。

构建失败时不创建版本提交或标签。若推送失败，本地已生成的版本提交和标签会保留，脚本打印精确的推送重试命令；先解决网络、权限或远端冲突，再使用该命令重试。不要直接再次执行 `make publish`，以免生成下一个版本。Git 服务不支持原子推送时命令会失败，不会退回分别推送。

首次加入发布脚本时，脚本及文档本身也需要先提交，才能使用 `make publish`。

## 手动构建与发布

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

Git 参考：[原子推送](https://git-scm.com/docs/git-push#Documentation/git-push.txt---atomic)、[仅提交指定路径](https://git-scm.com/docs/git-commit)。
