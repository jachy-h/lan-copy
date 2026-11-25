# GitHub 自动构建与发布

工作流：`.github/workflows/release.yml`。

| 操作 | 结果 |
| --- | --- |
| 推送 `main` | 在 Linux、Windows 上测试，通过后交叉构建带更新签名的 Windows amd64 和 macOS Apple Silicon 发布包 |
| 推送 `vx.y.z-build{c}` 标签 | 完成相同的测试与构建并发布 GitHub Release；Homebrew 公式由本机 `make publish` 在 Release 就绪后更新 |
| 在 Actions 手动运行 | 构建所选分支；选择版本标签时也会进入发布流程 |

CI 完全没有 macOS runner：`test` job 的矩阵只有 `ubuntu-latest` 和 `windows-latest`，`browser`、`build`、`release` 也都在 Ubuntu 上。所有 macOS 专属工作——darwin `go test`、`ruby -c`、`brew style`、`brew audit --strict --online` 与 Tap 公式更新——都在发布者本机完成（见下文 `make release-test`、`make homebrew-check`、`make homebrew`）。

这样做的原因：GitHub 托管的 macOS arm64 runner 容量极小。发布 `v0.16.0-build1` 时 `Update Homebrew tap` job 排队 15 分钟仍未获得机器（`The job was not acquired by Runner of type hosted even after multiple attempts`），最终被平台取消，Release 已公开而 Tap 公式停在旧版本。同一次运行的 ubuntu/windows job 都在 1.5~3 分钟内启动。

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

## Homebrew Tap 由本机更新

标签发布成功后，在本机执行 `make homebrew` 更新 Tap 仓库 `jachy-h/homebrew-jachy-h` 的 `Formula/lancopy.rb`：

1. `require-macos` 确认当前系统为 macOS；
2. `scripts/publish_homebrew.py` 检查 Tap 工作区干净，`git fetch` 后按需 `git merge --ff-only origin/main`；
3. 从本次 Release 下载 `SHA256SUMS`（默认路径，同时确认附件已上线），或用 `CHECKSUMS=release/SHA256SUMS` 指定本地文件；
4. 复用 `scripts/update_homebrew_formula.py` 的同一套逻辑，只改写 `url` 与 `sha256`；
5. 依次执行 `ruby -c`、`brew style`、`brew audit --strict --online --formula jachy-h/jachy-h/lancopy`（`HOMEBREW_AUDIT_ONLINE=0` 时只跑 `--strict`）；任一失败就恢复公式原文，工作区不留脏改动；
6. 校验通过后提交 `chore: update lancopy v<version>` 并推送。

前置条件：Homebrew 可用、已执行 `brew tap jachy-h/jachy-h`、本机 `git` 对该仓库有推送权限。`brew tap` 默认克隆 HTTPS remote，因此需要预先配好凭据：执行 `gh auth setup-git`，或把 Tap 的 remote 改为 SSH（`git -C "$(brew --repository)/Library/Taps/jachy-h/homebrew-jachy-h" remote set-url origin git@github.com:jachy-h/homebrew-jachy-h.git`）。推送失败时脚本会保留本地提交并打印这两种修法。旧工作流的 `HOMEBREW_TAP_TOKEN` secret 已作废：它只被 CI 的 `homebrew` job 使用，删除该 job 后不再有任何读取方，已在仓库 Settings 中删除。

```bash
make homebrew-check                          # 只读校验当前 Tap 公式，不提交
make homebrew DRY_RUN=1                      # 打印将写入的版本与校验和
make homebrew HOMEBREW_WAIT=900              # Release 未就绪时最多等待 15 分钟
make homebrew NO_PUSH=1                      # 只在本地提交，不推送
make homebrew CHECKSUMS=release/SHA256SUMS   # 用本地校验和（离线）
make homebrew HOMEBREW_AUDIT_ONLINE=0        # 网络受限时跳过 brew audit 的在线 URL 检查
```

`make publish` 已自动串联这一步：推送标签后按 `PUBLISH_HOMEBREW_WAIT`（默认 900 秒）等待 Release 附件上线，再调用 `make homebrew`。等待超时不会改动 Tap，可稍后单独重跑。Tap 仓库自己的 `validate` 工作流仍会在推送后复核一次。

## 一键发布：make publish

本地代码全部提交后，在 `main` 分支执行：

```bash
make publish
```

构建前会提示 `请确认是否发布 0.8.1-build4 版本 [y/n]:`（显示完整版本）。输入 `y` 后继续；输入 `n`、按 Ctrl+C 或关闭输入会取消，不构建、不修改版本、不提交、不创建标签或推送。其他输入会重新提示。

命令会依次完成：

1. 检查暂存区、工作区和未跟踪文件；有未提交改动就提示先 `git commit` 并停止。已忽略的 `.config/` 和 `release/` 不算未提交改动。
2. 检查当前分支为 `main`、远端可访问、没有落后或分叉，以及目标版本标签在本地和远端都不存在。
3. 在 macOS 上运行 `make release-test`：先确认平台是 macOS，再执行 `go vet ./...`、脚本测试与 `go test ./...`。这一步覆盖 CI 不再运行的 darwin 专属测试。
4. 调用签名构建，默认在同一基础版本上把构建号加一；`make publish VERSION=0.9.0` 开始新基础版本并重置为 `0.9.0-build1`，也可用 `VERSION=0.9.0-build3` 指定更高完整版本。测试失败时不会开始构建。
5. 构建成功后，仅将生成的 `VERSION` 提交为 `chore: release v0.8.1-build4`，再创建同名附注标签。构建期间出现其他改动会停止，不将其带入版本提交。
6. 将发布提交和版本标签通过一次 `git push --atomic` 推送至 `origin`，两者全部成功或全部拒绝。可通过 `PUBLISH_REMOTE` 指定其他远端。
7. GitHub Actions 在远端重新测试（Linux、Windows）、构建签名包，全部通过后公开 Release。
8. 等待该 Release 的 macOS 归档上线（`PUBLISH_HOMEBREW_WAIT`，默认 900 秒）后调用 `make homebrew` 更新并推送 Tap 公式；等待超时不会改动 Tap，可稍后单独执行 `make homebrew HOMEBREW_WAIT=900`。

该命令会实际创建 Git 提交、标签并推送。可以先执行 `make -n publish` 查看计划，预览不会构建或修改版本、提交、标签及远端。

构建失败时不创建版本提交或标签。`make publish` 必须在 macOS 上执行，其他系统会在 `release-test` 阶段直接停止。若推送失败，本地已生成的版本提交和标签会保留，脚本打印精确的推送重试命令；先解决网络、权限或远端冲突，再使用该命令重试。不要直接再次执行 `make publish`，以免生成下一个构建号。Git 服务不支持原子推送时命令会失败，不会退回分别推送。

首次加入发布脚本时，脚本及文档本身也需要先提交，才能使用 `make publish`。

## 手动构建与发布

本地 `make build` 在已保存版本上递增构建号；`make publish` 默认在同一基础版本上递增构建号并自动提交版本。CI 使用已提交的完整 `VERSION`，不递增、不自动提交，也不推送版本改动。这样分支构建、标签构建及失败重跑都使用同一个版本。

1. 本地确认 `VERSION` 是要发布的完整版本；如需升级基础版本，先更新 `VERSION` 为 `0.9.0`，或执行 `make build VERSION=0.9.0` 开始 `0.9.0-build1`；CI/重跑可用 `VERSION=0.9.0-build3` 原样指定。
2. 提交代码和 `VERSION`，推送 `main`。等待 Actions 构建成功，可先下载 Artifacts 验证。
3. 给对应提交添加同名版本标签并推送：

   ```bash
   git tag -a "v$(cat VERSION)" -m "Lan Copy $(cat VERSION)"
   git push origin "v$(cat VERSION)"
   ```

标签必须严格等于该提交的 `v` + `VERSION`，例如 `VERSION` 为 `0.8.1-build4` 时，标签必须是 `v0.8.1-build4`。标签不匹配会提前失败，防止附件版本与 Release 名称不一致。

发布 job 先核对下载包的校验和，再创建草稿、上传全部附件，最后公开 Release；上传失败时保留草稿以便重跑。已公开的 Release 不会在重跑时覆盖。只有发布 job 拥有 `contents: write` 权限，测试与构建 job 使用只读权限。Tap 更新已不在 CI 中执行，写 Tap 完全依赖本机凭据。

## 工作流启用

提交并推送工作流及相关脚本后生效。首次远端运行才能确认 GitHub runner 和仓库权限配置；本地检查不能替代一次实际 Actions 运行。

官方参考：[push 触发规则](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#push)、[GitHub CLI Release 命令](https://cli.github.com/manual/gh_release_create)。

Git 参考：[原子推送](https://git-scm.com/docs/git-push#Documentation/git-push.txt---atomic)、[仅提交指定路径](https://git-scm.com/docs/git-commit)。
